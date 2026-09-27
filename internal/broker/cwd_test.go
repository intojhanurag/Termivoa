package broker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func TestDirectoryFromReport(t *testing.T) {
	for payload, want := range map[string]string{
		"7;file://mac.local/Users/me/src":           "/Users/me/src",
		"7;file:///home/me/with%20space":            "/home/me/with space",
		"7;FILE://host/tmp":                         "/tmp",
		"7;file://host/":                            "/",
		"7;file:///C:/Users/me":                     "C:/Users/me",
		`9;9;"C:\Users\me\src"`:                     `C:\Users\me\src`,
		`9;9;C:\Users\me`:                           `C:\Users\me`,
		`9;9;"\\server\share\dir"`:                  `\\server\share\dir`,
		"9;9;\"/home/me\"":                          "/home/me",
		"7;file://host/%E6%9D%B1%E4%BA%AC":          "/東京",
		"7;http://host/tmp":                         "",
		"7;/tmp":                                    "",
		"7;file:relative/dir":                       "",
		"7;file://host/a/../etc":                    "",
		"7;file://host/tmp?x=1":                     "",
		"7;file://host/tmp#frag":                    "",
		"7;file://user@host/tmp":                    "",
		"7;file://host/a%0Ab":                       "",
		"7;file://host/a%1B%5D0;pwn%07":             "",
		"7;file://host/a%C2%9Bb":                    "",
		"7;file://host/a%00":                        "",
		"7;file://host/%zz":                         "",
		`9;9;"relative\dir"`:                        "",
		`9;9;""`:                                    "",
		"9;9;\"C:\\a\x1b[31m\"":                     "",
		"9;9;\"C:\\a\u202eb\"":                      "C:\\a\ufffdb", // A bidi override shows as U+FFFD.
		"7;file://host/a%C2%A0b":                    "/a\ufffdb",
		"9;notification":                            "",
		"2;title":                                   "",
		"7;file://host/" + strings.Repeat("a", 512): "",
	} {
		if got := directoryFromReport(payload); got != want {
			t.Errorf("%q: got %q, want %q", payload, got, want)
		}
	}
}

func TestCwdTrackerPrefersFreshReports(t *testing.T) {
	var tracker cwdTracker
	report := func(seq uint64, path string) terminal.DirectoryReport {
		return terminal.DirectoryReport{Payload: "7;file://host" + path, Seq: seq}
	}
	step := func(osPath string, report terminal.DirectoryReport, want string, changed bool) {
		t.Helper()
		if got := tracker.next(osPath, report); got != changed || tracker.current != want {
			t.Fatalf("next(%q, %+v) = %q changed=%t; want %q changed=%t", osPath, report, tracker.current, got, want, changed)
		}
	}
	step("", terminal.DirectoryReport{}, "", false)                        // Nothing known yet.
	step("/private/tmp", terminal.DirectoryReport{}, "/private/tmp", true) // OS only.
	step("/private/tmp", report(1, "/tmp"), "/tmp", true)                  // A fresh OSC 7 wins…
	step("/private/tmp", report(1, "/tmp"), "/tmp", false)                 // …and does not flap back.
	step("/Users/me", report(1, "/tmp"), "/Users/me", true)                // The OS changed without a report.
	step("/private/tmp", report(1, "/tmp"), "/private/tmp", true)          // OS mode follows the OS.
	step("", report(1, "/tmp"), "/private/tmp", false)                     // An unknown OS value keeps it.
	step("/x", report(2, "/a/../b"), "/x", true)                           // A malformed report is ignored.
	step("/x", report(3, "/x"), "/x", false)
	step("relative", report(3, "/x"), "/x", false) // An invalid OS value is unknown.

	// Windows: the OS never answers; the hook's OSC 9;9 is the only source.
	var windows cwdTracker
	windows.next("", terminal.DirectoryReport{Payload: `9;9;"C:\work"`, Seq: 1})
	windows.next("", terminal.DirectoryReport{Payload: `9;9;"C:\work"`, Seq: 1})
	if windows.current != `C:\work` {
		t.Fatalf("windows cwd = %q", windows.current)
	}
}

func TestWithDirectoryHookOnlyForPowerShellOnWindows(t *testing.T) {
	const (
		windowsPowerShell = `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
		pwsh              = `C:\Program Files\PowerShell\7\pwsh.exe`
	)
	hooked := []string{"-NoLogo", "-NoExit", "-Command", powerShellDirectoryHook}
	for _, test := range []struct {
		platform   store.Platform
		executable string
		arguments  []string
		want       []string
	}{
		{store.PlatformWindows, windowsPowerShell, []string{"-NoLogo"}, hooked},
		{store.PlatformWindows, pwsh, []string{"-NoLogo"}, hooked},
		{store.PlatformWindows, `C:/Tools/PWSH.EXE`, []string{"-nologo", "-NoExit"}, []string{"-nologo", "-NoExit", "-Command", powerShellDirectoryHook}},
		{store.PlatformWindows, pwsh, nil, []string{"-NoExit", "-Command", powerShellDirectoryHook}},
		// A profile with its own command or file keeps its arguments.
		{store.PlatformWindows, pwsh, []string{"-NoLogo", "-Command", "Write-Host hi"}, []string{"-NoLogo", "-Command", "Write-Host hi"}},
		{store.PlatformWindows, pwsh, []string{"-File", "start.ps1"}, []string{"-File", "start.ps1"}},
		{store.PlatformWindows, `C:\Windows\System32\cmd.exe`, []string{"/K"}, []string{"/K"}},
		{store.PlatformWSL, `C:\Windows\System32\wsl.exe`, nil, nil},
		{store.PlatformDarwin, "/bin/zsh", []string{"-l"}, []string{"-l"}},
		{store.PlatformDarwin, "/usr/local/bin/pwsh.exe", []string{"-NoLogo"}, []string{"-NoLogo"}},
	} {
		original := append([]string(nil), test.arguments...)
		got := withDirectoryHook(test.platform, test.executable, test.arguments)
		if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
			t.Errorf("%s %s %q = %q, want %q", test.platform, test.executable, test.arguments, got, test.want)
		}
		if strings.Join(test.arguments, "\x00") != strings.Join(original, "\x00") {
			t.Errorf("profile arguments were modified: %q", test.arguments)
		}
	}
	// One Windows command-line argument that PowerShell reads verbatim: no
	// double quotes (EscapeArg would backslash-escape them), no newlines or
	// other control characters, and the exact OSC 9;9 framing.
	for _, character := range powerShellDirectoryHook {
		if character == '"' || character < 0x20 || character == 0x7f {
			t.Fatalf("hook contains %q", character)
		}
	}
	for _, part := range []string{"$function:prompt", "function global:prompt", "[char]27", "']9;9;'", "[char]34", `$e + '\'`, "'FileSystem'", "ProviderPath"} {
		if !strings.Contains(powerShellDirectoryHook, part) {
			t.Errorf("hook lacks %q", part)
		}
	}
}

// OSC reports from the shell reach presence and the browser session list
// (the fake process has no OS reader, like Windows).
func TestCwdFollowsOSCReportsToTheList(t *testing.T) {
	fixture, session, process, service, _ := newAuthStreamFixture(t)
	phone := pairDevice(t, service, "Pocket")
	process.Emit([]byte("\x1b]7;file://host/Users/me/src/app\x07$ "))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Cwd == "/Users/me/src/app" })

	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	response := gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(phone.Credential))
	list, err := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](response)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range list.Sessions {
		if record.ID == session.id && (record.Cwd == nil || *record.Cwd != "/Users/me/src/app" || record.TitleIsCustom != nil) {
			t.Fatalf("record = %+v", record)
		}
	}

	process.Emit([]byte("\x1b]9;9;\"C:\\work\"\x1b\\"))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Cwd == `C:\work` })
	process.Emit([]byte("\x1b]7;file://host/a%0Ab\x07"))
	time.Sleep(cwdCheckDelay + cwdCheckEvery)
	if presence, err := session.Presence(context.Background(), false); err != nil || presence.Cwd != `C:\work` {
		t.Fatalf("hostile report changed cwd: %q, %v", presence.Cwd, err)
	}
}
