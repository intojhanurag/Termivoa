package ptyhost

import (
	"encoding/binary"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Rows copied from a little-endian Ubuntu 24.04 /proc/net/tcp{,6}; inodes
// renumbered. Every Linux host this project supports is little-endian.
const (
	procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1001 1 0000000000000000 100 0 0 10 0
   1: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1002 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1F90 0100007F:D2F0 01 00000000:00000000 00:00000000 00000000  1000        0 1003 1 0000000000000000 20 4 30 10 -1
   3: 0164A8C0:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 1004 1 0000000000000000 100 0 0 10 0
   4: 0100007F:1F91 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 0 1 0000000000000000 100 0 0 10 0
   5: garbage
`
	procNetTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000001000000:0BB9 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2001 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000000000000:1389 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2002 1 0000000000000000 100 0 0 10 0
   2: 0000000000000000FFFF00000100007F:138A 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2003 1 0000000000000000 100 0 0 10 0
`
)

func requireLittleEndian(t *testing.T) {
	t.Helper()
	if binary.NativeEndian.Uint16([]byte{1, 0}) != 1 {
		t.Skip("fixtures are from a little-endian host")
	}
}

func TestParseProcNetTCPKeepsOnlyListenRows(t *testing.T) {
	requireLittleEndian(t)
	var got []procSocket
	collect := func(socket procSocket) { got = append(got, socket) }
	if err := parseProcNetTCP(strings.NewReader(procNetTCP), collect); err != nil {
		t.Fatal(err)
	}
	if err := parseProcNetTCP(strings.NewReader(procNetTCP6), collect); err != nil {
		t.Fatal(err)
	}
	want := []procSocket{
		{1001, netip.MustParseAddr("127.0.0.1"), 8080},
		{1002, netip.MustParseAddr("0.0.0.0"), 22},
		{1004, netip.MustParseAddr("192.168.100.1"), 3000},
		{2001, netip.MustParseAddr("::1"), 3001},
		{2002, netip.MustParseAddr("::"), 5001},
		{2003, netip.MustParseAddr("::ffff:127.0.0.1"), 5002},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sockets = %v\nwant      %v", got, want)
	}
}

func TestSocketInode(t *testing.T) {
	for link, want := range map[string]uint64{
		"socket:[1001]": 1001, "socket:[]": 0, "socket:[12": 0, "pipe:[1001]": 0, "/dev/pts/3": 0, "anon_inode:[eventfd]": 0,
	} {
		if got := socketInode(link); got != want {
			t.Errorf("socketInode(%q) = %d, want %d", link, got, want)
		}
	}
}

// procListeners maps LISTEN socket inodes to the tree's processes through
// their fd links. A listener of a process outside the tree, a connected
// socket, and a vanished process are not reported.
func TestProcListenersMapsInodesToTreeProcesses(t *testing.T) {
	requireLittleEndian(t)
	root := t.TempDir()
	write := func(name, data string) {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	links := func(pid int, targets ...string) {
		directory := filepath.Join(root, strconv.Itoa(pid), "fd")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for fd, target := range targets {
			if err := os.Symlink(target, filepath.Join(directory, strconv.Itoa(fd))); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("net/tcp", procNetTCP)
	write("net/tcp6", procNetTCP6)
	links(100, "/dev/pts/3", "/dev/pts/3", "pipe:[77]", "socket:[1001]", "socket:[1003]")
	links(101, "socket:[2001]", "socket:[2002]", "socket:[2003]")
	links(500, "socket:[1004]") // listens on the LAN address, but outside the tree

	listeners, err := procListeners(root, []int{100, 101, 102})
	if err != nil {
		t.Fatal(err)
	}
	want := []Listener{
		{100, netip.MustParseAddr("127.0.0.1"), 8080},
		{101, netip.MustParseAddr("::1"), 3001},
		{101, netip.MustParseAddr("::"), 5001},
		{101, netip.MustParseAddr("::ffff:127.0.0.1"), 5002},
	}
	if !reflect.DeepEqual(listeners, want) {
		t.Fatalf("listeners = %v\nwant        %v", listeners, want)
	}
	got := PreviewablePorts(listeners, []int{100, 101})
	wantPorts := []PortTarget{{3001, "::1"}, {5001, "::1"}, {5002, "127.0.0.1"}, {8080, "127.0.0.1"}}
	if !reflect.DeepEqual(got, wantPorts) {
		t.Fatalf("previewable = %v, want %v", got, wantPorts)
	}

	// IPv6 disabled: no tcp6 table is not an error. No tcp table is.
	if err := os.Remove(filepath.Join(root, "net", "tcp6")); err != nil {
		t.Fatal(err)
	}
	if listeners, err := procListeners(root, []int{100, 101}); err != nil || len(listeners) != 1 {
		t.Fatalf("without tcp6 = %v, %v", listeners, err)
	}
	if err := os.Remove(filepath.Join(root, "net", "tcp")); err != nil {
		t.Fatal(err)
	}
	if _, err := procListeners(root, []int{100}); err == nil {
		t.Fatal("missing /proc/net/tcp was not an error")
	}
}
