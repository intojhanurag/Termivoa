//go:build windows

package gitchanges

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type writeProcessTree struct{ job windows.Handle }

func prepareWriteProcess(command *exec.Cmd) (writeProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return writeProcessTree{}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return writeProcessTree{}, err
	}
	tree := writeProcessTree{job: job}
	// Match the existing PTY ownership order: no user code runs before Job
	// assignment. A cancellation before assignment also kills suspended git.
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	command.Cancel = func() error { return tree.kill(command) }
	return tree, nil
}

func (tree writeProcessTree) attach(command *exec.Cmd) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err = windows.AssignProcessToJobObject(tree.job, process); err != nil {
		return err
	}
	return resumeWriteProcess(uint32(command.Process.Pid))
}

func (tree writeProcessTree) kill(command *exec.Cmd) error {
	err := windows.TerminateJobObject(tree.job, 1)
	// If context cancellation races Start/assignment, git is suspended and may
	// not belong to the Job yet. Killing it directly cannot leave descendants.
	if command.Process != nil {
		if direct := command.Process.Kill(); !errors.Is(direct, os.ErrProcessDone) {
			err = errors.Join(err, direct)
		}
	}
	return err
}

// The Job reports active processes, so orphans cannot be mistaken for a
// completed operation. Termination is asynchronous; wait at most one second.
func (tree writeProcessTree) close() error {
	deadline := time.Now().Add(time.Second)
	var accounting struct {
		TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
		TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
	}
	var err error
	for {
		err = windows.QueryInformationJobObject(tree.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
		if err != nil || accounting.ActiveProcesses == 0 {
			break
		}
		if time.Now().After(deadline) {
			err = errors.New("gitchanges: git process cleanup did not finish")
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errors.Join(err, windows.CloseHandle(tree.job))
}

// Go's exec process has no retained primary-thread handle. This is the same
// bounded Toolhelp thread snapshot/resume sequence used by PTY startup.
func resumeWriteProcess(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err = windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	resumed := false
	for {
		if entry.OwnerProcessID == processID {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return openErr
			}
			_, resumeErr := windows.ResumeThread(thread)
			closeErr := windows.CloseHandle(thread)
			if resumeErr != nil || closeErr != nil {
				return errors.Join(resumeErr, closeErr)
			}
			resumed = true
		}
		if err = windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return err
		}
	}
	if !resumed {
		return errors.New("gitchanges: suspended git has no thread to resume")
	}
	return nil
}
