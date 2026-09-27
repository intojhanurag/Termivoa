//go:build windows

package ptyhost

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tcpTableOwnerPIDListener = 3 // TCP_TABLE_OWNER_PID_LISTENER
	mibTCPStateListen        = 2 // MIB_TCP_STATE_LISTEN
	tcp4RowSize              = 24
	tcp6RowSize              = 56
	maxTCPTableSize          = 8 << 20
	maxJobProcesses          = 1024
)

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// jobProcessIDList is JOBOBJECT_BASIC_PROCESS_ID_LIST with room for
// maxJobProcesses entries.
type jobProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIdsInList  uint32
	ProcessIdList             [maxJobProcesses]uintptr
}

// sessionListeners returns the processes in the session's Job Object and the
// TCP listeners of the whole system; PreviewablePorts keeps only the job's.
// ponytail: reap closes the job handle concurrently. ListeningPorts discards
// results once the session is done, but a query already in flight may see a
// closed (or reused) handle; the window is a few instructions wide. Guard the
// handle with a lock if that ever matters.
func sessionListeners(ctx context.Context, tree platformProcessTree) ([]int, []Listener, error) {
	if tree.job == 0 {
		return nil, nil, nil
	}
	var list jobProcessIDList
	err := windows.QueryInformationJobObject(tree.job, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil)
	// ERROR_MORE_DATA still fills the list as far as it fits. Every listed PID
	// is in the job, so a partial list can only hide ports.
	if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
		return nil, nil, err
	}
	count := min(int(list.NumberOfProcessIdsInList), maxJobProcesses)
	pids := make([]int, 0, count)
	for _, pid := range list.ProcessIdList[:count] {
		pids = append(pids, int(pid))
	}
	if len(pids) == 0 {
		return nil, nil, nil
	}

	var listeners []Listener
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		table, err := tcpListenerTable(family)
		if err != nil {
			return nil, nil, err
		}
		listeners = append(listeners, decodeTCPTable(table, family)...)
	}
	return pids, listeners, nil
}

func tcpListenerTable(family uint32) ([]byte, error) {
	size := uint32(64 << 10)
	for {
		buffer := make([]byte, size)
		result, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)),
			0, uintptr(family), tcpTableOwnerPIDListener, 0)
		switch windows.Errno(result) {
		case windows.ERROR_SUCCESS:
			return buffer[:min(int(size), len(buffer))], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			if size > maxTCPTableSize {
				return nil, errors.New("ptyhost: TCP table is too large")
			}
			// The table may grow before the next call.
			size += 4 << 10
		default:
			return nil, windows.Errno(result)
		}
	}
}

// decodeTCPTable decodes a MIB_TCPTABLE_OWNER_PID (AF_INET) or
// MIB_TCP6TABLE_OWNER_PID (AF_INET6): a DWORD row count followed by rows.
// Addresses and ports are in network byte order; other fields are host
// (little-endian) order.
func decodeTCPTable(table []byte, family uint32) []Listener {
	rowSize := tcp4RowSize
	if family == windows.AF_INET6 {
		rowSize = tcp6RowSize
	}
	if len(table) < 4 {
		return nil
	}
	count := int(binary.LittleEndian.Uint32(table))
	rows := table[4:]
	count = min(count, len(rows)/rowSize)
	listeners := make([]Listener, 0, count)
	for i := range count {
		row := rows[i*rowSize : (i+1)*rowSize]
		var state, pid uint32
		var addr netip.Addr
		var port uint16
		if family == windows.AF_INET6 {
			addr = netip.AddrFrom16([16]byte(row[0:16]))
			port = binary.BigEndian.Uint16(row[20:22])
			state = binary.LittleEndian.Uint32(row[48:52])
			pid = binary.LittleEndian.Uint32(row[52:56])
		} else {
			state = binary.LittleEndian.Uint32(row[0:4])
			addr = netip.AddrFrom4([4]byte(row[4:8]))
			port = binary.BigEndian.Uint16(row[8:10])
			pid = binary.LittleEndian.Uint32(row[20:24])
		}
		if state == mibTCPStateListen {
			listeners = append(listeners, Listener{PID: int(pid), Addr: addr, Port: port})
		}
	}
	return listeners
}
