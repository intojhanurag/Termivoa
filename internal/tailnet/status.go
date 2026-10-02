// Package tailnet reads `tailscale status --json` for multi-computer trust
// checks (docs/multi-computer.md, "Same tailnet is not same owner"). A Status
// is an immutable, indexed snapshot; a Monitor refreshes it asynchronously.
// Tailscale identity only narrows access here; it is never authentication.
package tailnet

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	ErrNotFound   = errors.New("tailnet: no computer in this tailnet has that name")
	ErrAmbiguous  = errors.New("tailnet: more than one computer in this tailnet has that name")
	ErrOtherOwner = errors.New("tailnet: the computer belongs to another Tailscale user")
	ErrTagged     = errors.New("tailnet: the computer is a tagged node")
)

// Node is one Tailscale node. Host is its MagicDNS name without the trailing
// dot, in lowercase. UserID is the owner's stable Tailscale user ID, the only
// value owners are compared by. Owner is the owner's login name, for display
// and storage only: it is not unique across identity providers. Owner is
// empty for tagged nodes.
type Node struct {
	ID     string
	Host   string
	UserID int64
	Owner  string
	Tags   []string
}

// Tagged reports a tagged node. Tagged nodes share one placeholder owner, so
// owners are never compared for them.
func (node Node) Tagged() bool { return len(node.Tags) > 0 }

// Wire returns the protocol form. Owner is absent for tagged nodes.
func (node Node) Wire() protocol.TailnetNode {
	wire := protocol.TailnetNode{ID: node.ID, Tags: append([]string{}, node.Tags...)}
	if node.Owner != "" && !node.Tagged() {
		owner := node.Owner
		wire.Owner = &owner
	}
	return wire
}

// Origin is the node's canonical https origin.
func (node Node) Origin() string { return "https://" + node.Host }

// Status is an immutable snapshot of `tailscale status --json`. Peers holds
// only nodes of this computer's own tailnet: shared-in nodes and nodes of
// other tailnets are left out, so they can never be resolved or trusted.
type Status struct {
	BackendState string
	Self         Node
	// Suffix is the tailnet's MagicDNS suffix, for example tail1234.ts.net.
	Suffix string
	peers  map[string]Node // by Host
}

type rawPeer struct {
	ID         string
	DNSName    string
	UserID     int64
	Tags       []string
	ShareeNode bool
}

// ParseStatus parses `tailscale status --json`. It is lenient about missing
// parts (a logged-out client has no Self DNS name); only malformed JSON fails.
// Peers with an invalid ID, host or tags are dropped.
func ParseStatus(data []byte) (*Status, error) {
	var raw struct {
		BackendState   string
		Self           *rawPeer
		Peer           map[string]rawPeer
		User           map[string]struct{ LoginName string }
		MagicDNSSuffix string
		CurrentTailnet *struct{ MagicDNSSuffix string }
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse tailscale status: %w", err)
	}
	status := &Status{BackendState: raw.BackendState, peers: map[string]Node{}}
	status.Suffix = strings.ToLower(strings.TrimSuffix(raw.MagicDNSSuffix, "."))
	if status.Suffix == "" && raw.CurrentTailnet != nil {
		status.Suffix = strings.ToLower(strings.TrimSuffix(raw.CurrentTailnet.MagicDNSSuffix, "."))
	}
	node := func(peer rawPeer) Node {
		owner := ""
		if len(peer.Tags) == 0 {
			owner = raw.User[strconv.FormatInt(peer.UserID, 10)].LoginName
		}
		return Node{ID: peer.ID, Host: strings.ToLower(strings.TrimSuffix(peer.DNSName, ".")),
			UserID: peer.UserID, Owner: owner, Tags: peer.Tags}
	}
	if raw.Self != nil {
		status.Self = node(*raw.Self)
	}
	if status.Suffix == "" {
		return status, nil
	}
	for _, peer := range raw.Peer {
		candidate := node(peer)
		if peer.ShareeNode || !strings.HasSuffix(candidate.Host, "."+status.Suffix) ||
			protocol.ValidateComputerOrigin(candidate.Origin()) != nil || validNode(candidate) != nil ||
			candidate.Host == status.Self.Host {
			continue
		}
		status.peers[candidate.Host] = candidate
	}
	return status, nil
}

// validNode mirrors the protocol's TailnetNode bounds, so every node this
// package hands out encodes into a valid message.
func validNode(node Node) error {
	if node.ID == "" || len(node.ID) > 64 || strings.Trim(node.ID, idCharacters) != "" ||
		len(node.Owner) > protocol.MaxTailnetOwnerBytes || strings.TrimSpace(node.Owner) != node.Owner ||
		!utf8.ValidString(node.Owner) || strings.ContainsFunc(node.Owner, unicode.IsControl) ||
		(!node.Tagged() && (node.Owner == "" || node.UserID == 0)) || len(node.Tags) > protocol.MaxTailnetTags {
		return errors.New("tailnet: node is outside protocol bounds")
	}
	for _, tag := range node.Tags {
		name, ok := strings.CutPrefix(tag, "tag:")
		if !ok || name == "" || len(name) > 60 || strings.ContainsFunc(name, func(r rune) bool { return r < '!' || r > '~' }) {
			return errors.New("tailnet: node tag is outside protocol bounds")
		}
	}
	return nil
}

const idCharacters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"

// Peer returns the own-tailnet node whose MagicDNS name is host.
func (status *Status) Peer(host string) (Node, bool) {
	node, ok := status.peers[host]
	return node, ok
}

// Resolve finds the app node for pterm pair --via. via is a canonical
// computer origin, or one DNS label matched only against the first label of
// own-tailnet MagicDNS names (never HostName, never shared-in nodes). More
// than one match is refused.
func (status *Status) Resolve(via string) (Node, error) {
	if host, ok := strings.CutPrefix(via, "https://"); ok {
		if protocol.ValidateComputerOrigin(via) != nil {
			return Node{}, ErrNotFound
		}
		node, found := status.peers[host]
		if !found {
			return Node{}, ErrNotFound
		}
		return node, nil
	}
	var matches []Node
	for host, node := range status.peers {
		if first, _, _ := strings.Cut(host, "."); first == via {
			matches = append(matches, node)
		}
	}
	switch len(matches) {
	case 0:
		return Node{}, ErrNotFound
	case 1:
		return matches[0], nil
	default:
		return Node{}, ErrAmbiguous
	}
}

// CheckOwner applies the owner rule to an app node: a user-owned node must
// have this computer's owner, compared by stable user ID (login names are
// display text and not unique across identity providers). When either node
// is tagged, owners are never compared and the node is accepted only with
// allowTagged.
func (status *Status) CheckOwner(node Node, allowTagged bool) error {
	if node.Tagged() || status.Self.Tagged() {
		if !allowTagged {
			return ErrTagged
		}
		return nil
	}
	if node.UserID == 0 || node.UserID != status.Self.UserID {
		return ErrOtherOwner
	}
	return nil
}

// Verdict is the trust result for one bound app origin.
type Verdict int

const (
	// Trusted: the origin's host is still the pinned node, with an allowed owner.
	Trusted Verdict = iota
	// Changed: a different node now holds the origin's name. Devices bound to
	// the origin must be revoked (never re-bound).
	Changed
	// Untrusted: the name is gone or the owner no longer matches. Refuse, but
	// do not revoke.
	Untrusted
)

// Trust checks a bound app origin against its pinned node ID and the node's
// tags when it was pinned. Tagged nodes were accepted with an explicit
// allowTagged at pairing, so only the pinned ID matters for their owner; a
// node whose tag set changed since then (gained, lost or swapped a tag) is
// refused, because the ACL rules that apply to it changed without the
// owner's approval.
func (status *Status) Trust(origin, nodeID string, tags []string) Verdict {
	node, ok := status.peers[strings.TrimPrefix(origin, "https://")]
	switch {
	case !ok || !strings.HasPrefix(origin, "https://"):
		return Untrusted
	case node.ID != nodeID:
		return Changed
	case !SameTags(node.Tags, tags), status.CheckOwner(node, true) != nil:
		return Untrusted
	}
	return Trusted
}

// SameTags compares two tag lists as sets, ignoring order.
func SameTags(first, second []string) bool {
	return slices.Equal(slices.Compact(slices.Sorted(slices.Values(first))), slices.Compact(slices.Sorted(slices.Values(second))))
}
