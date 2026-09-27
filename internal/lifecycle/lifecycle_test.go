package lifecycle

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

var macHost = Host{
	OS:         "darwin",
	Executable: `/Users/ada/Apps/R&D <"x">/Phone Terminal.app/Contents/MacOS/pterm`,
	DataDir:    "/Users/ada/Library/Application Support/Phone Terminal",
	HomeDir:    "/Users/ada",
	UID:        501,
}

var windowsHost = Host{
	OS:         "windows",
	Executable: `C:\Users\Ada & Co\AppData\Local\Programs\Phone Terminal\versions\0.1.0\pterm.exe`,
	DataDir:    `C:\Users\Ada & Co\AppData\Local\Phone Terminal`,
	User:       `WORKSTATION\Ada & Co`,
}

const wantBrokerPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.phone-terminal.broker</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/ada/Apps/R&amp;D &lt;&#34;x&#34;&gt;/Phone Terminal.app/Contents/MacOS/pterm</string>
		<string>__broker</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>/Users/ada/Library/Application Support/Phone Terminal/logs/broker.log</string>
	<key>StandardErrorPath</key>
	<string>/Users/ada/Library/Application Support/Phone Terminal/logs/broker.log</string>
</dict>
</plist>
`

const wantGatewayTask = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Phone Terminal gateway for the signed-in user.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>WORKSTATION\Ada &amp; Co</UserId>
      <Delay>PT10S</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>WORKSTATION\Ada &amp; Co</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>true</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%SystemRoot%\System32\conhost.exe</Command>
      <Arguments>--headless &#34;C:\Users\Ada &amp; Co\AppData\Local\Programs\Phone Terminal\versions\0.1.0\pterm.exe&#34; __gateway</Arguments>
    </Exec>
  </Actions>
</Task>
`

func TestMacPlistExactContent(t *testing.T) {
	data, text, err := macHost.Content(Services[0])
	if err != nil {
		t.Fatal(err)
	}
	if text != wantBrokerPlist || string(data) != wantBrokerPlist {
		t.Fatalf("plist mismatch:\n%s", text)
	}
	if got := macHost.File(Services[1]); got != "/Users/ada/Library/LaunchAgents/dev.phone-terminal.gateway.plist" {
		t.Fatalf("gateway plist path = %q", got)
	}
	_, gateway, _ := macHost.Content(Services[1])
	if !strings.Contains(gateway, "<string>__gateway</string>") || !strings.Contains(gateway, "logs/gateway.log") {
		t.Fatal("gateway plist does not run gateway mode with its own log")
	}
}

func TestWindowsTaskExactContentAndEncoding(t *testing.T) {
	data, text, err := windowsHost.Content(Services[1])
	if err != nil {
		t.Fatal(err)
	}
	if text != wantGatewayTask {
		t.Fatalf("task XML mismatch:\n%s", text)
	}
	if data[0] != 0xff || data[1] != 0xfe || len(data)%2 != 0 {
		t.Fatal("task XML is not UTF-16LE with a byte-order mark")
	}
	units := make([]uint16, 0, len(data)/2-1)
	for index := 2; index < len(data); index += 2 {
		units = append(units, uint16(data[index])|uint16(data[index+1])<<8)
	}
	if string(utf16.Decode(units)) != wantGatewayTask {
		t.Fatal("UTF-16 file does not decode to the task XML")
	}
	_, broker, _ := windowsHost.Content(Services[0])
	if strings.Contains(broker, "<Delay>") || !strings.Contains(broker, "pterm.exe&#34; __broker</Arguments>") {
		t.Fatal("broker task must start at logon without delay in broker mode")
	}
	if got := windowsHost.TaskName(Services[0]); got != "Phone Terminal Broker (Ada & Co)" {
		t.Fatalf("task name = %q", got)
	}
	if got := windowsHost.File(Services[0]); got != `C:\Users\Ada & Co\AppData\Local\Phone Terminal\startup\broker.xml` {
		t.Fatalf("task file = %q", got)
	}
}

func TestHostRejectsUnsafePaths(t *testing.T) {
	for name, host := range map[string]Host{
		"relative mac":      {OS: "darwin", Executable: "pterm", DataDir: "/d", HomeDir: "/h"},
		"newline mac":       {OS: "darwin", Executable: "/a\n/pterm", DataDir: "/d", HomeDir: "/h"},
		"nul mac":           {OS: "darwin", Executable: "/a\x00", DataDir: "/d", HomeDir: "/h"},
		"relative windows":  {OS: "windows", Executable: `pterm.exe`, DataDir: `C:\d`, User: `D\u`},
		"quote windows":     {OS: "windows", Executable: `C:\a"b\pterm.exe`, DataDir: `C:\d`, User: `D\u`},
		"bad user windows":  {OS: "windows", Executable: `C:\pterm.exe`, DataDir: `C:\d`, User: `D\u\x`},
		"unsupported linux": {OS: "linux", Executable: "/pterm", DataDir: "/d"},
	} {
		if _, _, err := host.Content(Services[0]); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func commands(plan Plan) [][]string {
	var result [][]string
	for _, step := range plan.Steps {
		if step.Command != nil {
			result = append(result, step.Command)
		}
	}
	return result
}

func statuses(host Host, fileMatches, registered bool) []Status {
	var result []Status
	for _, service := range Services {
		result = append(result, Status{Service: service, File: host.File(service), FileExists: fileMatches, FileMatches: fileMatches, Registered: registered})
	}
	return result
}

func TestMacInstallAndUninstallCommands(t *testing.T) {
	fresh, err := macHost.InstallPlan(statuses(macHost, false, false))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"launchctl", "bootstrap", "gui/501", "/Users/ada/Library/LaunchAgents/dev.phone-terminal.broker.plist"},
		{"launchctl", "bootstrap", "gui/501", "/Users/ada/Library/LaunchAgents/dev.phone-terminal.gateway.plist"},
	}
	if !reflect.DeepEqual(commands(fresh), want) || fresh.ChangesRunningBroker {
		t.Fatalf("fresh install = %q changes=%v", commands(fresh), fresh.ChangesRunningBroker)
	}
	if fresh.Steps[0].Mkdir != "/Users/ada/Library/Application Support/Phone Terminal/logs" || fresh.Steps[1].Write == "" {
		t.Fatalf("fresh install must create the log directory then write the plist: %+v", fresh.Steps[:2])
	}

	current, _ := macHost.InstallPlan(statuses(macHost, true, true))
	if len(current.Steps) != 0 {
		t.Fatalf("current registration planned changes: %v", current.Steps)
	}

	changed, _ := macHost.InstallPlan(statuses(macHost, false, true))
	want = [][]string{
		{"launchctl", "bootout", "gui/501/dev.phone-terminal.broker"},
		{"launchctl", "bootstrap", "gui/501", "/Users/ada/Library/LaunchAgents/dev.phone-terminal.broker.plist"},
		{"launchctl", "bootout", "gui/501/dev.phone-terminal.gateway"},
		{"launchctl", "bootstrap", "gui/501", "/Users/ada/Library/LaunchAgents/dev.phone-terminal.gateway.plist"},
	}
	if !reflect.DeepEqual(commands(changed), want) || !changed.ChangesRunningBroker {
		t.Fatalf("changed install = %q changes=%v", commands(changed), changed.ChangesRunningBroker)
	}

	removal, _ := macHost.UninstallPlan(statuses(macHost, true, true), nil)
	want = [][]string{
		{"launchctl", "bootout", "gui/501/dev.phone-terminal.gateway"},
		{"launchctl", "bootout", "gui/501/dev.phone-terminal.broker"},
	}
	if !reflect.DeepEqual(commands(removal), want) || !removal.ChangesRunningBroker {
		t.Fatalf("uninstall = %q", commands(removal))
	}
	if removal.Steps[1].Remove != "/Users/ada/Library/LaunchAgents/dev.phone-terminal.gateway.plist" {
		t.Fatalf("uninstall must remove the plist after bootout: %+v", removal.Steps)
	}
}

func TestWindowsInstallAndUninstallCommands(t *testing.T) {
	broker := "Phone Terminal Broker (Ada & Co)"
	gateway := "Phone Terminal Gateway (Ada & Co)"
	brokerXML := `C:\Users\Ada & Co\AppData\Local\Phone Terminal\startup\broker.xml`
	gatewayXML := `C:\Users\Ada & Co\AppData\Local\Phone Terminal\startup\gateway.xml`
	fresh, err := windowsHost.InstallPlan(statuses(windowsHost, false, false))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"schtasks", "/Create", "/TN", broker, "/XML", brokerXML, "/F"},
		{"schtasks", "/Run", "/TN", broker},
		{"schtasks", "/Create", "/TN", gateway, "/XML", gatewayXML, "/F"},
		{"schtasks", "/Run", "/TN", gateway},
	}
	if !reflect.DeepEqual(commands(fresh), want) || fresh.ChangesRunningBroker {
		t.Fatalf("fresh install = %q", commands(fresh))
	}
	if fresh.Steps[0].Mkdir != `C:\Users\Ada & Co\AppData\Local\Phone Terminal\startup` {
		t.Fatalf("first step = %+v", fresh.Steps[0])
	}
	changed, _ := windowsHost.InstallPlan(statuses(windowsHost, false, true))
	if !changed.ChangesRunningBroker {
		t.Fatal("replacing a registered broker task must be flagged")
	}

	removal, _ := windowsHost.UninstallPlan(statuses(windowsHost, true, true), []string{`C:\Users\Ada & Co\AppData\Local\Phone Terminal`})
	want = [][]string{
		{"schtasks", "/End", "/TN", gateway},
		{"schtasks", "/Delete", "/TN", gateway, "/F"},
		{"schtasks", "/End", "/TN", broker},
		{"schtasks", "/Delete", "/TN", broker, "/F"},
	}
	if !reflect.DeepEqual(commands(removal), want) {
		t.Fatalf("uninstall = %q", commands(removal))
	}
	last := removal.Steps[len(removal.Steps)-1]
	if last.RemoveTree != `C:\Users\Ada & Co\AppData\Local\Phone Terminal` {
		t.Fatalf("data deletion must be last: %+v", last)
	}
}

func TestUninstallRefusesUnexpectedDataDirectories(t *testing.T) {
	for _, path := range []string{"/Users/ada", "/", "/Users/ada/Library/Application Support"} {
		if _, err := macHost.UninstallPlan(nil, []string{path}); err == nil {
			t.Errorf("accepted deletion of %q", path)
		}
	}
}

func TestDescribePrintsExactChanges(t *testing.T) {
	plan, _ := windowsHost.InstallPlan(statuses(windowsHost, false, false))
	var output bytes.Buffer
	if err := Describe(plan, &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		`  run schtasks /Create /TN "Phone Terminal Broker (Ada & Co)" /XML "C:\Users\Ada & Co\AppData\Local\Phone Terminal\startup\broker.xml" /F`,
		"      | <Task version=\"1.2\"",
		"      |       <Command>%SystemRoot%\\System32\\conhost.exe</Command>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("description lacks %q:\n%s", want, text)
		}
	}
}

type fakeRunner struct {
	calls      [][]string
	registered map[string]bool
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	call := append([]string{name}, args...)
	runner.calls = append(runner.calls, call)
	switch args[0] {
	case "print":
		if !runner.registered[args[1]] {
			return "Could not find service", errors.New("exit status 113")
		}
	case "bootstrap":
		runner.registered["gui/"+args[1][4:]+"/"+strings.TrimSuffix(filepath.Base(args[2]), ".plist")] = true
	case "bootout":
		delete(runner.registered, args[1])
	}
	return "", nil
}

// TestApplyRoundTripInTemporaryHome runs install and uninstall against a
// temporary home with a fake launchctl; nothing touches the real user.
func TestApplyRoundTripInTemporaryHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix paths")
	}
	root := t.TempDir()
	host := Host{OS: "darwin", Executable: "/opt/pterm", HomeDir: filepath.Join(root, "home"), DataDir: filepath.Join(root, "data", "Phone Terminal"), UID: 501}
	runner := &fakeRunner{registered: map[string]bool{}}
	ctx := context.Background()

	status, err := host.Inspect(ctx, runner)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := host.InstallPlan(status)
	if err := Apply(ctx, runner, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(host.LogDir())
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("log directory = %v, %v", info, err)
	}
	written, _ := os.ReadFile(host.File(Services[0]))
	_, want, _ := host.Content(Services[0])
	if string(written) != want {
		t.Fatal("installed plist differs from generated content")
	}

	status, _ = host.Inspect(ctx, runner)
	if plan, _ := host.InstallPlan(status); len(plan.Steps) != 0 {
		t.Fatalf("second install is not idempotent: %v", plan.Steps)
	}

	plan, _ = host.UninstallPlan(status, []string{host.DataDir})
	if err := Apply(ctx, runner, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{host.File(Services[0]), host.File(Services[1]), host.DataDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists", path)
		}
	}
	if len(runner.registered) != 0 {
		t.Fatalf("services still registered: %v", runner.registered)
	}
}

func TestServeFindings(t *testing.T) {
	for name, test := range map[string]struct {
		json       string
		configured bool
		problems   int
	}{
		"empty":       {json: "{}"},
		"no output":   {json: ""},
		"configured":  {json: `{"TCP":{"443":{"HTTPS":true}},"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}}}}`, configured: true},
		"conflict":    {json: `{"TCP":{"443":{"HTTPS":true}},"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}}}}`, problems: 1},
		"tcp forward": {json: `{"TCP":{"443":{"TCPForward":"127.0.0.1:22"}}}`, problems: 1},
		"funnel":      {json: `{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8787"}}}},"AllowFunnel":{"h.ts.net:443":true}}`, configured: true, problems: 1},
	} {
		configured, problems, err := ServeFindings([]byte(test.json))
		if err != nil || configured != test.configured || len(problems) != test.problems {
			t.Errorf("%s: configured=%v problems=%q err=%v", name, configured, problems, err)
		}
	}
	status, err := ParseTailscaleStatus([]byte(`{"BackendState":"Running","Self":{"DNSName":"mac.tail1234.ts.net."}}`))
	if err != nil || status.BackendState != "Running" || status.DNSName != "mac.tail1234.ts.net" {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestExecutableProblems(t *testing.T) {
	for path, bad := range map[string]bool{
		"/Applications/Phone Terminal.app/Contents/MacOS/pterm":                false,
		"/Volumes/Phone Terminal/Phone Terminal.app/Contents/MacOS/pterm":      true,
		"/private/var/folders/x/AppTranslocation/y/d/Phone Terminal.app/pterm": true,
		"/var/folders/x/T/go-build123/b001/exe/pterm":                          true,
		"/Users/ada/Applications/Phone Terminal.app/Contents/MacOS/pterm":      false,
	} {
		if got := len(ExecutableProblems("darwin", path)) > 0; got != bad {
			t.Errorf("%s: problem=%v", path, got)
		}
	}
	if len(ExecutableProblems("windows", `C:\Users\A\AppData\Local\Temp\x\pterm.exe`)) == 0 {
		t.Error("windows temp path accepted")
	}
}

// TestPlistParsesWithPlutil proves escaping yields a valid property list
// whose program path round-trips exactly.
func TestPlistParsesWithPlutil(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("plutil is macOS-only")
	}
	_, text, _ := macHost.Content(Services[0])
	path := filepath.Join(t.TempDir(), "broker.plist")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v\n%s", err, output)
	}
	output, err := exec.Command(plutil, "-extract", "ProgramArguments.0", "raw", "-o", "-", path).Output()
	if err != nil || strings.TrimSuffix(string(output), "\n") != macHost.Executable {
		t.Fatalf("program path round trip = %q, %v", output, err)
	}
}

// TestTaskXMLIsWellFormed proves escaping round-trips the user and command.
func TestTaskXMLIsWellFormed(t *testing.T) {
	_, text, _ := windowsHost.Content(Services[0])
	var task struct {
		UserID    string `xml:"Principals>Principal>UserId"`
		Arguments string `xml:"Actions>Exec>Arguments"`
	}
	decoder := xml.NewDecoder(strings.NewReader(text))
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := decoder.Decode(&task); err != nil {
		t.Fatal(err)
	}
	if task.UserID != windowsHost.User || task.Arguments != `--headless "`+windowsHost.Executable+`" __broker` {
		t.Fatalf("decoded task = %+v", task)
	}
}

func TestUnloadCommandsAreExactAndPerUser(t *testing.T) {
	for host, want := range map[*Host]string{
		&macHost:     "launchctl bootout gui/501/dev.phone-terminal.gateway",
		&windowsHost: `schtasks /Change /TN "Phone Terminal Gateway (Ada & Co)" /Disable`,
	} {
		if got := DisplayCommand(host.UnloadCommand(Services[1])); got != want {
			t.Errorf("unload command = %q, want %q", got, want)
		}
	}
}
