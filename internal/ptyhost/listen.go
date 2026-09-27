package ptyhost

import (
	"bufio"
	"bytes"
	"context"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// MaxPreviewPorts caps how many ports one session may expose for preview.
const MaxPreviewPorts = 8

// maxTreeProcesses bounds the work one ListeningPorts call may do. Processes
// beyond it are ignored, which can only hide ports, never expose foreign ones.
const maxTreeProcesses = 4096

// Listener is one TCP socket in LISTEN state.
type Listener struct {
	PID  int
	Addr netip.Addr
	Port uint16
}

// PortTarget is a previewable port and the loopback address a proxy dials for it.
type PortTarget struct {
	Port uint16
	Host string // "127.0.0.1" or "::1"
}

// ListeningPorts lists previewable ports of this session's process tree,
// verified from the operating system on every call. Ownership is checked
// twice: the OS query is restricted to the tree, and PreviewablePorts drops
// any listener whose PID is not in it. It returns (nil, nil) once the session
// has ended and on platforms without support.
func (p *Process) ListeningPorts(ctx context.Context) ([]PortTarget, error) {
	// Once the leader is reaped its PID, which is also the session ID the tree
	// is built from, may be reused by an unrelated process (and on Windows the
	// Job handle is closed). Check before enumerating, and discard a result
	// that raced with the end of the session.
	if p.isDone() {
		return nil, nil
	}
	tree, listeners, err := sessionListeners(ctx, p.tree)
	if err != nil || p.isDone() {
		return nil, err
	}
	return PreviewablePorts(listeners, tree), nil
}

func (p *Process) isDone() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// PreviewablePorts keeps listeners whose PID is in tree and whose address is
// loopback (127.0.0.0/8 or ::1) or a wildcard (0.0.0.0 or ::); IPv4-mapped
// addresses are unmapped first. Other addresses, such as a LAN or Tailscale
// address, are dropped: the proxy dials loopback, which would reach a
// different socket, possibly one owned by another process. It returns one
// entry per port, sorted by port, at most MaxPreviewPorts. Host is
// "127.0.0.1" when any kept listener for the port is IPv4, else "::1".
func PreviewablePorts(listeners []Listener, tree []int) []PortTarget {
	owned := make(map[int]bool, len(tree))
	for _, pid := range tree {
		owned[pid] = true
	}
	ipv4 := map[uint16]bool{}
	for _, listener := range listeners {
		addr := listener.Addr.Unmap()
		if !owned[listener.PID] || listener.Port == 0 || addr.Zone() != "" {
			continue
		}
		if !addr.IsUnspecified() && !(addr.IsLoopback() && (addr.Is4() || addr == netip.IPv6Loopback())) {
			continue
		}
		ipv4[listener.Port] = ipv4[listener.Port] || addr.Is4()
	}
	ports := slices.Sorted(maps.Keys(ipv4))
	if len(ports) > MaxPreviewPorts {
		ports = ports[:MaxPreviewPorts]
	}
	targets := make([]PortTarget, 0, len(ports))
	for _, port := range ports {
		host := "::1"
		if ipv4[port] {
			host = "127.0.0.1"
		}
		targets = append(targets, PortTarget{Port: port, Host: host})
	}
	return targets
}

// descendants returns roots and every process whose parent chain reaches a
// root, sorted and without duplicates. parents maps PID to parent PID.
func descendants(parents map[int]int, roots []int) []int {
	children := map[int][]int{}
	for pid, parent := range parents {
		if pid != parent {
			children[parent] = append(children[parent], pid)
		}
	}
	seen := map[int]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid <= 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		queue = append(queue, children[pid]...)
	}
	tree := make([]int, 0, len(seen))
	for pid := range seen {
		tree = append(tree, pid)
	}
	slices.Sort(tree)
	return tree
}

// parseLsof parses `lsof -F ptn` output: a "p<pid>" line starts a process,
// then each file has "f<fd>", "t<IPv4|IPv6>" and "n<address>:<port>" lines.
// "*" is the wildcard of the socket's family. Malformed lines are ignored.
func parseLsof(output []byte) []Listener {
	var listeners []Listener
	pid, family := 0, ""
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, family = 0, ""
			if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
				pid = parsed
			}
		case 'f':
			family = ""
		case 't':
			family = value
		case 'n':
			if listener, ok := parseLsofName(pid, family, value); ok {
				listeners = append(listeners, listener)
			}
		}
	}
	return listeners
}

func parseLsofName(pid int, family, name string) (Listener, bool) {
	colon := strings.LastIndexByte(name, ':')
	if pid == 0 || colon < 0 || strings.Contains(name, "->") {
		return Listener{}, false
	}
	port, err := strconv.ParseUint(name[colon+1:], 10, 16)
	if err != nil {
		return Listener{}, false
	}
	host := name[:colon]
	var addr netip.Addr
	switch {
	case host == "*" && family == "IPv4":
		addr = netip.IPv4Unspecified()
	case host == "*" && family == "IPv6":
		addr = netip.IPv6Unspecified()
	case family == "IPv4":
		addr, err = netip.ParseAddr(host)
	case family == "IPv6" && strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]"):
		addr, err = netip.ParseAddr(host[1 : len(host)-1])
	default:
		return Listener{}, false
	}
	if err != nil {
		return Listener{}, false
	}
	return Listener{PID: pid, Addr: addr, Port: uint16(port)}, true
}
