package ptyhost

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procSocket is one LISTEN row of /proc/net/tcp or /proc/net/tcp6.
type procSocket struct {
	inode uint64
	addr  netip.Addr
	port  uint16
}

// tcpListenState is TCP_LISTEN in the kernel's hexadecimal "st" column.
const tcpListenState = "0A"

// parseProcNetTCP calls found for each LISTEN socket in a /proc/net/tcp or
// tcp6 table. Rows look like
//
//	0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000  0 123456 ...
//
// with local address, remote address, state, queues, timer, retransmits, UID,
// timeout and inode. The port is big-endian hexadecimal. The address is the
// raw network-order bytes printed as native-endian 32-bit words, so each word
// is reversed on the little-endian CPUs Linux hosts use. Malformed rows and
// the header are ignored.
func parseProcNetTCP(table io.Reader, found func(procSocket)) error {
	scanner := bufio.NewScanner(table)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[3] != tcpListenState {
			continue
		}
		hexAddr, hexPort, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		port, portErr := strconv.ParseUint(hexPort, 16, 16)
		inode, inodeErr := strconv.ParseUint(fields[9], 10, 64)
		addr, addrOK := procAddress(hexAddr)
		if portErr != nil || inodeErr != nil || inode == 0 || !addrOK {
			continue
		}
		found(procSocket{inode: inode, addr: addr, port: uint16(port)})
	}
	return scanner.Err()
}

func procAddress(text string) (netip.Addr, bool) {
	raw, err := hex.DecodeString(text)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.Addr{}, false
	}
	for index := 0; index < len(raw); index += 4 {
		binary.NativeEndian.PutUint32(raw[index:], binary.BigEndian.Uint32(raw[index:]))
	}
	addr, _ := netip.AddrFromSlice(raw)
	return addr, true
}

// socketInode returns N for a /proc/<pid>/fd link "socket:[N]", else 0.
func socketInode(link string) uint64 {
	text, ok := strings.CutPrefix(link, "socket:[")
	if !ok || !strings.HasSuffix(text, "]") {
		return 0
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(text, "]"), 10, 64)
	if err != nil {
		return 0
	}
	return inode
}

// procListeners reads root/net/tcp{,6} and root/<pid>/fd for each pid. A
// missing tcp6 table (IPv6 disabled) or a process that has exited is skipped.
func procListeners(root string, pids []int) ([]Listener, error) {
	sockets := map[uint64]procSocket{}
	for _, table := range []string{"tcp", "tcp6"} {
		file, err := os.Open(filepath.Join(root, "net", table))
		if errors.Is(err, os.ErrNotExist) && table == "tcp6" {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = parseProcNetTCP(file, func(socket procSocket) { sockets[socket.inode] = socket })
		_ = file.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(sockets) == 0 {
		return nil, nil
	}
	var listeners []Listener
	for _, pid := range pids {
		directory := filepath.Join(root, strconv.Itoa(pid), "fd")
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			link, err := os.Readlink(filepath.Join(directory, entry.Name()))
			if err != nil {
				continue
			}
			if socket, ok := sockets[socketInode(link)]; ok {
				listeners = append(listeners, Listener{PID: pid, Addr: socket.addr, Port: socket.port})
			}
		}
	}
	return listeners, nil
}
