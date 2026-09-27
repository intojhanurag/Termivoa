//go:build windows

package ptyhost

import (
	"encoding/binary"
	"net/netip"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDecodeTCPTable(t *testing.T) {
	v4 := make([]byte, 4+2*tcp4RowSize)
	binary.LittleEndian.PutUint32(v4, 3) // claims one row more than present
	row := v4[4:]
	binary.LittleEndian.PutUint32(row[0:], mibTCPStateListen)
	copy(row[4:8], []byte{127, 0, 0, 1})
	binary.BigEndian.PutUint16(row[8:], 5173)
	binary.LittleEndian.PutUint32(row[20:], 4242)
	row = v4[4+tcp4RowSize:]
	binary.LittleEndian.PutUint32(row[0:], 5) // established: skipped
	binary.LittleEndian.PutUint32(row[20:], 4242)

	v6 := make([]byte, 4+tcp6RowSize)
	binary.LittleEndian.PutUint32(v6, 1)
	row = v6[4:]
	row[15] = 1 // ::1
	binary.BigEndian.PutUint16(row[20:], 8080)
	binary.LittleEndian.PutUint32(row[48:], mibTCPStateListen)
	binary.LittleEndian.PutUint32(row[52:], 77)

	got := append(decodeTCPTable(v4, windows.AF_INET), decodeTCPTable(v6, windows.AF_INET6)...)
	want := []Listener{
		{4242, netip.MustParseAddr("127.0.0.1"), 5173},
		{77, netip.MustParseAddr("::1"), 8080},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := decodeTCPTable([]byte{1}, windows.AF_INET); len(got) != 0 {
		t.Fatalf("short table decoded to %v", got)
	}
}
