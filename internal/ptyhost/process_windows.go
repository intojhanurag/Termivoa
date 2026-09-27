//go:build windows

package ptyhost

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type platformProcessTree struct {
	job windows.Handle
}

// hostEnvironment starts from the user's default environment block, not from
// the broker's inherited environment, so registry variables reach sessions
// but launcher variables do not. If the block is unavailable it falls back to
// the allowlisted host variables.
func hostEnvironment(host []string) []string {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		return windowsEnvironment(nil, host)
	}
	defer token.Close()
	userDefault, err := token.Environ(false)
	if err != nil {
		userDefault = nil
	}
	return windowsEnvironment(userDefault, host)
}

func configureCommand(cmd *exec.Cmd) {
	// xpty creates the process through CreateProcess and honors CreationFlags.
	// Starting suspended closes the otherwise unavoidable window in which the
	// child could create a descendant before the broker assigns it to its Job.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
}

func attachProcessTree(cmd *exec.Cmd) (platformProcessTree, error) {
	if cmd.Process == nil {
		return platformProcessTree{}, errors.New("ptyhost: process was not started")
	}

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return platformProcessTree{}, err
	}
	closeJob := true
	defer func() {
		if closeJob {
			_ = windows.CloseHandle(job)
		}
	}()

	information := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	information.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&information)),
		uint32(unsafe.Sizeof(information)),
	); err != nil {
		return platformProcessTree{}, err
	}

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		return platformProcessTree{}, err
	}
	defer windows.CloseHandle(process)

	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		return platformProcessTree{}, err
	}
	if err := resumeProcessThreads(uint32(cmd.Process.Pid)); err != nil {
		return platformProcessTree{}, err
	}

	closeJob = false
	return platformProcessTree{job: job}, nil
}

func cleanupUnattachedProcess(cmd *exec.Cmd) (bool, error) {
	killErr := cmd.Process.Kill()
	if killErr == nil || errors.Is(killErr, os.ErrProcessDone) {
		return true, nil
	}

	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return false, errors.Join(killErr, err)
	}
	defer windows.CloseHandle(process)
	if err := windows.TerminateProcess(process, 1); err != nil {
		if status, waitErr := windows.WaitForSingleObject(process, 0); waitErr == nil && status == windows.WAIT_OBJECT_0 {
			return true, nil
		}
		return false, errors.Join(killErr, err)
	}
	return true, nil
}

func resumeProcessThreads(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
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

		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return err
		}
	}
	if !resumed {
		return errors.New("ptyhost: suspended process has no thread to resume")
	}
	return nil
}

func terminateProcessTree(tree platformProcessTree) error {
	if tree.job == 0 {
		return errors.New("ptyhost: process was not started")
	}
	return windows.TerminateJobObject(tree.job, 1)
}

func forceTerminateProcessTree(tree platformProcessTree) error {
	return terminateProcessTree(tree)
}

func cleanupExitedProcessTree(platformProcessTree) error {
	// reap closes a KILL_ON_JOB_CLOSE job before publishing Process.done.
	return nil
}

func closeProcessTree(tree platformProcessTree) error {
	if tree.job != 0 {
		return windows.CloseHandle(tree.job)
	}
	return nil
}
