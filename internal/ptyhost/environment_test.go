package ptyhost

import (
	"reflect"
	"testing"
)

func TestInheritedEnvironmentUnixAllowlist(t *testing.T) {
	host := []string{
		"HOME=/Users/a", "USER=a", "LOGNAME=a", "SHELL=/bin/zsh", "PATH=/usr/bin:/bin",
		"TMPDIR=/tmp/x", "SSH_AUTH_SOCK=/tmp/agent", "__CF_USER_TEXT_ENCODING=0x1F5:0x0:0x0",
		"LANG=en_US.UTF-8", "LANGUAGE=en", "LC_ALL=C", "LC_CTYPE=UTF-8", "TZ=UTC",
		"CLAUDECODE=1", "CLAUDE_CODE_X=1", "CLAUDE_CODE_MESSAGING_TOKEN=secret",
		"TERM=xterm-kitty", "COLORTERM=24bit", "TERM_PROGRAM=iTerm.app", "ITERM_SESSION_ID=w0",
		"VSCODE_GIT_IPC_HANDLE=/tmp/v", "TMUX=/tmp/tmux", "SHLVL=3", "PWD=/elsewhere",
		"XPC_SERVICE_NAME=x", "home=/lower", "Path=C:\\x", "SystemRoot=C:\\Windows", "noequals",
	}
	want := host[:13]
	if got := inheritedEnvironment(host, "darwin"); !reflect.DeepEqual(got, want) {
		t.Fatalf("inherited = %q, want %q", got, want)
	}
	if got := inheritedEnvironment(nil, "linux"); got == nil || len(got) != 0 {
		t.Fatalf("empty host environment = %#v, want non-nil empty", got)
	}
}

func TestInheritedEnvironmentWindowsAllowlist(t *testing.T) {
	keep := []string{
		`SystemRoot=C:\Windows`, `windir=C:\Windows`, `SystemDrive=C:`, `ComSpec=C:\Windows\system32\cmd.exe`,
		`Path=C:\Windows\system32`, `PATHEXT=.COM;.EXE`, `TEMP=C:\t`, `TMP=C:\t`,
		`USERPROFILE=C:\Users\a`, `APPDATA=C:\Users\a\AppData\Roaming`, `LOCALAPPDATA=C:\Users\a\AppData\Local`,
		`PUBLIC=C:\Users\Public`, `ALLUSERSPROFILE=C:\ProgramData`, `ProgramData=C:\ProgramData`,
		`ProgramFiles=C:\Program Files`, `ProgramFiles(x86)=C:\Program Files (x86)`, `ProgramW6432=C:\Program Files`,
		`CommonProgramFiles=C:\Program Files\Common Files`, `CommonProgramFiles(x86)=C:\Program Files (x86)\Common Files`,
		`CommonProgramW6432=C:\Program Files\Common Files`, `HOMEDRIVE=C:`, `HOMEPATH=\Users\a`,
		`USERNAME=a`, `USERDOMAIN=D`, `COMPUTERNAME=PC`, `OS=Windows_NT`, `NUMBER_OF_PROCESSORS=8`,
		`PROCESSOR_ARCHITECTURE=AMD64`, `processor_level=6`, `PSModulePath=C:\m`, `systemroot=C:\Windows`,
	}
	drop := []string{
		`=C:=C:\dir`, `CLAUDE_CODE_X=1`, `TERM_PROGRAM=vscode`, `ITERM_SESSION_ID=w0`, `VSCODE_PID=1`,
		`WT_SESSION=abc`, `PROMPT=$P$G`, `HOME=C:\Users\a`, `LANG=C`, `SHELL=x`,
	}
	got := inheritedEnvironment(append(append([]string(nil), keep...), drop...), "windows")
	if !reflect.DeepEqual(got, keep) {
		t.Fatalf("inherited = %q, want %q", got, keep)
	}
}
