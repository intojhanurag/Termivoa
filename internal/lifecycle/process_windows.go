//go:build windows

package lifecycle

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// DetachedAttributes starts a process without the launcher's console.
func DetachedAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

// GroupAttributes starts a child in its own console process group, so the
// console's Ctrl+C does not reach it and Interrupt can target it alone.
func GroupAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// Interrupt sends Ctrl+Break to a child started with GroupAttributes. Go
// delivers it to the child as os.Interrupt.
func Interrupt(process *os.Process) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(process.Pid))
}
