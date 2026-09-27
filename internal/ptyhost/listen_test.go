package ptyhost

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestParseLsof(t *testing.T) {
	output := []byte("p100\nf10\ntIPv4\nn127.0.0.1:5173\nf11\ntIPv6\nn[::1]:5173\n" +
		"f12\ntIPv4\nn*:3000\nf13\ntIPv6\nn*:3000\nf14\ntIPv6\nn[::]:8080\n" +
		"p200\nf3\ntIPv4\nn192.168.1.5:22\n" +
		// Malformed: no port, bad port, name before type, unknown family,
		// a connection, a bad pid, and garbage.
		"f4\ntIPv4\nn127.0.0.1\nf5\ntIPv4\nn127.0.0.1:99999\nf6\nn127.0.0.1:1\n" +
		"f7\ntunix\nn*:2\nf8\ntIPv4\nn127.0.0.1:1->127.0.0.1:2\n" +
		"pxyz\nf1\ntIPv4\nn127.0.0.1:4\n\nzzz\n")
	want := []Listener{
		{100, netip.MustParseAddr("127.0.0.1"), 5173},
		{100, netip.MustParseAddr("::1"), 5173},
		{100, netip.MustParseAddr("0.0.0.0"), 3000},
		{100, netip.MustParseAddr("::"), 3000},
		{100, netip.MustParseAddr("::"), 8080},
		{200, netip.MustParseAddr("192.168.1.5"), 22},
	}
	if got := parseLsof(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseLsof =\n%v\nwant\n%v", got, want)
	}
	if got := parseLsof(nil); len(got) != 0 {
		t.Fatalf("parseLsof(nil) = %v", got)
	}
}

func TestPreviewablePorts(t *testing.T) {
	l := func(pid int, addr string, port uint16) Listener {
		return Listener{PID: pid, Addr: netip.MustParseAddr(addr), Port: port}
	}
	tree := []int{10, 11}
	for _, test := range []struct {
		name      string
		listeners []Listener
		want      []PortTarget
	}{
		{"loopback v4", []Listener{l(10, "127.0.0.1", 5173)}, []PortTarget{{5173, "127.0.0.1"}}},
		{"loopback 127/8", []Listener{l(10, "127.0.0.2", 5173)}, []PortTarget{{5173, "127.0.0.1"}}},
		{"wildcard v4", []Listener{l(11, "0.0.0.0", 3000)}, []PortTarget{{3000, "127.0.0.1"}}},
		{"loopback v6", []Listener{l(10, "::1", 8080)}, []PortTarget{{8080, "::1"}}},
		{"wildcard v6", []Listener{l(10, "::", 8080)}, []PortTarget{{8080, "::1"}}},
		{"mapped loopback", []Listener{l(10, "::ffff:127.0.0.1", 4000)}, []PortTarget{{4000, "127.0.0.1"}}},
		{"mapped wildcard", []Listener{l(10, "::ffff:0.0.0.0", 4000)}, []PortTarget{{4000, "127.0.0.1"}}},
		{"not owned", []Listener{l(99, "127.0.0.1", 5173), l(99, "0.0.0.0", 3000)}, []PortTarget{}},
		{"lan", []Listener{l(10, "192.168.1.5", 5173)}, []PortTarget{}},
		{"tailscale", []Listener{l(10, "100.101.102.103", 5173), l(10, "fd7a:115c:a1e0::1", 5174)}, []PortTarget{}},
		{"mapped lan", []Listener{l(10, "::ffff:192.168.1.5", 5173)}, []PortTarget{}},
		{"zoned", []Listener{l(10, "fe80::1%lo0", 5173)}, []PortTarget{}},
		{"port zero", []Listener{l(10, "127.0.0.1", 0)}, []PortTarget{}},
		{"dedupe prefers v4", []Listener{l(10, "::1", 5173), l(11, "127.0.0.1", 5173), l(10, "::", 5173)}, []PortTarget{{5173, "127.0.0.1"}}},
		{"sorted", []Listener{l(10, "::1", 9000), l(10, "127.0.0.1", 80)}, []PortTarget{{80, "127.0.0.1"}, {9000, "::1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := PreviewablePorts(test.listeners, tree); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}

	var many []Listener
	for port := uint16(20); port > 0; port-- {
		many = append(many, l(10, "127.0.0.1", 1000+port))
	}
	got := PreviewablePorts(many, tree)
	if len(got) != MaxPreviewPorts || got[0].Port != 1001 || got[MaxPreviewPorts-1].Port != 1000+MaxPreviewPorts {
		t.Fatalf("cap: got %v", got)
	}
}

func TestDescendants(t *testing.T) {
	parents := map[int]int{
		1:  0,
		50: 1, // unrelated
		51: 50,
		10: 1, // session leader
		11: 10,
		12: 11, // detached with setsid, still a descendant
		13: 12,
		20: 1, // session member reparented to init
		21: 20,
		30: 30, // self-parented entries must not loop
	}
	if got, want := descendants(parents, []int{10, 20, 10, 0}), []int{10, 11, 12, 13, 20, 21}; !reflect.DeepEqual(got, want) {
		t.Fatalf("descendants = %v, want %v", got, want)
	}
	if got := descendants(parents, nil); len(got) != 0 {
		t.Fatalf("descendants(no roots) = %v", got)
	}
}
