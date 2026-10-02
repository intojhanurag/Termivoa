package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// tailnetStatus is the subset of `tailscale status --json` that the doctor's
// multi-computer checks read: this computer, its peers, their owners and the
// tailnet's MagicDNS suffix.
// ponytail: replace with internal/tailnet at integration
type tailnetStatus struct {
	Self           tailnetPeer
	Peer           map[string]tailnetPeer
	User           map[string]struct{ LoginName string } // key: decimal UserID
	CurrentTailnet *struct{ MagicDNSSuffix string }
}

type tailnetPeer struct {
	ID      string
	DNSName string
	UserID  int64
	Tags    []string
}

func parseTailnetStatus(data []byte) (tailnetStatus, error) {
	var status tailnetStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return tailnetStatus{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	return status, nil
}

func dnsHost(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) }

// owner names a tailnet user for doctor output, escaped like other text that
// does not come from this program.
func (status tailnetStatus) owner(userID int64) string {
	if user, ok := status.User[strconv.FormatInt(userID, 10)]; ok && user.LoginName != "" {
		return strconv.QuoteToGraphic(user.LoginName)
	}
	return "user " + strconv.FormatInt(userID, 10)
}

// tailnetFindings checks each unrevoked device's pinned app node against the
// current tailnet: the app host must be in this tailnet, its pinned node must
// still exist and still own that name, and, when neither node is tagged, it
// must have this computer's owner. Each origin and node pair is reported once.
func tailnetFindings(status tailnetStatus, devices []protocol.AdminRPCDevice) []string {
	suffix := ""
	if status.CurrentTailnet != nil {
		suffix = dnsHost(status.CurrentTailnet.MagicDNSSuffix)
	}
	byID, byName := map[string]tailnetPeer{}, map[string]tailnetPeer{}
	for _, peer := range status.Peer {
		byID[peer.ID], byName[dnsHost(peer.DNSName)] = peer, peer
	}
	checked := map[string]bool{}
	var findings []string
	for _, device := range devices {
		if device.Revoked || device.AppOrigin == nil || device.AppNode == nil {
			continue
		}
		origin, pinned := *device.AppOrigin, device.AppNode.ID
		if checked[origin+" "+pinned] {
			continue
		}
		checked[origin+" "+pinned] = true
		host := strings.TrimPrefix(origin, "https://")
		peer, found := byID[pinned]
		named, hasName := byName[host]
		switch {
		case suffix == "" || !strings.HasSuffix(host, "."+suffix):
			findings = append(findings, origin+": different tailnet; not supported")
		case hasName && named.ID != pinned:
			findings = append(findings, origin+": node ID changed (reused name?)")
		case !found:
			findings = append(findings, fmt.Sprintf("%s: pinned node %s is gone; phones bound to it stop working", origin, pinned))
		case len(peer.Tags) == 0 && len(status.Self.Tags) == 0 && peer.UserID != status.Self.UserID:
			findings = append(findings, fmt.Sprintf("%s: owner mismatch (%s; this computer: %s)",
				origin, status.owner(peer.UserID), status.owner(status.Self.UserID)))
		}
	}
	return findings
}

// otherServeHandlers lists what else Tailscale Serve offers on this
// computer's own HTTPS origin: handlers on <host>:443 other than "/" proxying
// the gateway, and a TCP forward on 443. lifecycle.ServeFindings already
// counts each as a problem; this names them for the home-computer warning.
func otherServeHandlers(data []byte, dnsName string) []string {
	var raw struct {
		TCP map[string]struct{ TCPForward string }
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	var found []string
	if forward := raw.TCP["443"].TCPForward; forward != "" {
		found = append(found, "TCP forward to "+forward)
	}
	for hostPort, web := range raw.Web {
		if strings.ToLower(hostPort) != dnsHost(dnsName)+":443" {
			continue
		}
		for path, handler := range web.Handlers {
			if path != "/" || strings.TrimSuffix(handler.Proxy, "/") != strings.TrimSuffix(lifecycle.LocalURL, "/") {
				found = append(found, path)
			}
		}
	}
	slices.Sort(found)
	return found
}

// otherHandlersWarning explains why other Serve handlers on this origin
// matter once this computer serves the app for other computers.
func otherHandlersWarning(dnsName string, handlers []string) string {
	return fmt.Sprintf("tailscale serve: other handlers share this computer's origin (%s): %s. "+
		"As the home computer, any of them would control every computer paired through it; "+
		"remove them before using this computer as home.", dnsHost(dnsName), strings.Join(handlers, ", "))
}

// inspectMultiComputer prints the protocol revision and, when the broker
// answers, its bound app origins (the gateway's CORS list), the app origins
// of pending pairings, and tailnet and owner mismatches of bound devices.
func inspectMultiComputer(ctx context.Context, env lifecycleEnv, report hostReport, output io.Writer) (problems int) {
	fmt.Fprintf(output, "  protocol revision %d (oldest supported %d)\n", protocol.ProtocolRevision, protocol.MinProtocolRevision)
	if !report.broker || report.listErr != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout+2*time.Second)
	defer cancel()
	admin, err := localclient.DialAdmin(ctx, env.paths.RuntimeDir, buildID)
	if err != nil {
		fmt.Fprintf(output, "  ! multi-computer: cannot read devices: %v\n", err)
		return 1
	}
	defer admin.Close()
	devices, err := admin.Devices(ctx)
	var pending []protocol.AdminRPCPendingPairing
	if err == nil {
		pending, err = admin.PendingPairings(ctx)
	}
	if err != nil {
		fmt.Fprintf(output, "  ! multi-computer: cannot read devices: %v\n", err)
		return 1
	}
	var bound, pairing []string
	for _, device := range devices {
		if !device.Revoked && device.AppOrigin != nil {
			bound = append(bound, *device.AppOrigin)
		}
	}
	for _, item := range pending {
		if item.AppOrigin != nil {
			pairing = append(pairing, *item.AppOrigin)
		}
	}
	slices.Sort(bound)
	slices.Sort(pairing)
	if bound = slices.Compact(bound); len(bound) == 0 {
		fmt.Fprintln(output, "  multi-computer: no bound app origins (CORS off)")
	} else {
		fmt.Fprintf(output, "  multi-computer: CORS list (bound app origins): %s\n", strings.Join(bound, ", "))
	}
	if pairing = slices.Compact(pairing); len(pairing) > 0 {
		fmt.Fprintf(output, "  multi-computer: pairing CORS list (pending pairings): %s\n", strings.Join(pairing, ", "))
	}
	if report.tailscale.tailnet == nil {
		if len(bound) > 0 {
			fmt.Fprintln(output, "  multi-computer: tailnet and owner checks skipped (no Tailscale status)")
		}
		return 0
	}
	for _, finding := range tailnetFindings(*report.tailscale.tailnet, devices) {
		fmt.Fprintf(output, "  ! multi-computer: %s\n", finding)
		problems++
	}
	return problems
}
