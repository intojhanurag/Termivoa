// Package lifecycle models per-user startup registration for the separately
// supervised broker and gateway processes. It generates macOS LaunchAgent
// property lists and Windows Scheduled Task XML, plans install/uninstall steps
// and applies them only through an injected command runner. Nothing here
// requests elevation, registers a system-wide service, or changes Tailscale.
package lifecycle

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
)

const (
	// BrokerMode and GatewayMode must equal the internal process modes in
	// cmd/pterm; a cmd test asserts that.
	BrokerMode  = "__broker"
	GatewayMode = "__gateway"

	// LocalURL is the fixed production gateway loopback address.
	LocalURL = "http://127.0.0.1:8787/"
	// ServeCommand is printed for the owner to run. pterm never runs it.
	ServeCommand = "tailscale serve --bg --https=443 http://127.0.0.1:8787"
)

type Service struct {
	Name  string // "broker" or "gateway"
	Label string // macOS launchd label
	Mode  string // internal pterm process mode
}

// Services lists startup order: the gateway requires a reachable broker.
var Services = []Service{
	{Name: "broker", Label: "dev.phone-terminal.broker", Mode: BrokerMode},
	{Name: "gateway", Label: "dev.phone-terminal.gateway", Mode: GatewayMode},
}

// Host describes the current user's installation. All fields are explicit so
// cross-platform content can be generated and tested on either OS.
type Host struct {
	OS         string // "darwin" or "windows"
	Executable string // absolute installed pterm path
	DataDir    string // production data dir: logs (darwin), task XML (windows)
	HomeDir    string // darwin: parent of Library/LaunchAgents
	UID        int    // darwin: gui/<uid> launchd domain
	User       string // windows: DOMAIN\user owning the tasks
}

func (h Host) Validate() error {
	switch h.OS {
	case "darwin":
		if h.UID < 0 {
			return errors.New("lifecycle: macOS user ID must not be negative")
		}
		if err := h.checkPath("home", h.HomeDir); err != nil {
			return err
		}
	case "windows":
		if h.User == "" || strings.ContainsAny(h.User, "/:*?\"<>|\x00") || strings.Count(h.User, `\`) > 1 {
			return errors.New("lifecycle: Windows user must be DOMAIN\\user")
		}
	default:
		return fmt.Errorf("lifecycle: unsupported startup platform %q", h.OS)
	}
	if err := h.checkPath("executable", h.Executable); err != nil {
		return err
	}
	return h.checkPath("data", h.DataDir)
}

// checkPath rejects anything that XML escaping would silently alter (control
// characters are replaced, not escaped) or that is not absolute for h.OS.
func (h Host) checkPath(kind, path string) error {
	if !utf8.ValidString(path) || strings.IndexFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return fmt.Errorf("lifecycle: %s path contains control characters or invalid UTF-8", kind)
	}
	absolute := strings.HasPrefix(path, "/")
	if h.OS == "windows" {
		absolute = len(path) > 3 && path[1] == ':' && path[2] == '\\' || strings.HasPrefix(path, `\\`)
		if strings.Contains(path, `"`) {
			return fmt.Errorf("lifecycle: %s path must not contain quotes", kind)
		}
	}
	if !absolute {
		return fmt.Errorf("lifecycle: %s path must be absolute", kind)
	}
	return nil
}

func (h Host) join(elements ...string) string {
	separator := "/"
	if h.OS == "windows" {
		separator = `\`
	}
	for index := range elements {
		if index > 0 {
			elements[index] = strings.Trim(elements[index], separator)
		} else {
			elements[index] = strings.TrimRight(elements[index], separator)
		}
	}
	return strings.Join(elements, separator)
}

// LogDir holds macOS launchd stdout/stderr and detached-start logs.
func (h Host) LogDir() string { return h.join(h.DataDir, "logs") }

func (h Host) startupDir() string { return h.join(h.DataDir, "startup") }

// File is the registration source file for a service.
func (h Host) File(service Service) string {
	if h.OS == "windows" {
		return h.join(h.startupDir(), service.Name+".xml")
	}
	return h.join(h.HomeDir, "Library", "LaunchAgents", service.Label+".plist")
}

// TaskName is the per-user Windows task name. Root-folder task names are
// machine-wide, so the account name keeps two users from colliding.
func (h Host) TaskName(service Service) string {
	user := h.User[strings.LastIndex(h.User, `\`)+1:]
	return "Phone Terminal " + strings.ToUpper(service.Name[:1]) + service.Name[1:] + " (" + user + ")"
}

func (h Host) domain() string { return "gui/" + strconv.Itoa(h.UID) }

// Content returns the exact registration bytes and their readable form.
func (h Host) Content(service Service) (data []byte, text string, err error) {
	if err := h.Validate(); err != nil {
		return nil, "", err
	}
	if h.OS == "windows" {
		text = h.taskXML(service)
		return utf16File(text), text, nil
	}
	text = h.plist(service)
	return []byte(text), text, nil
}

func escape(value string) string {
	var builder strings.Builder
	_ = xml.EscapeText(&builder, []byte(value))
	return builder.String()
}

func (h Host) plist(service Service) string {
	log := escape(h.join(h.LogDir(), service.Name+".log"))
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + service.Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + escape(h.Executable) + `</string>
		<string>` + service.Mode + `</string>
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
	<string>` + log + `</string>
	<key>StandardErrorPath</key>
	<string>` + log + `</string>
</dict>
</plist>
`
}

// taskXML runs pterm through a headless console host so no console window
// appears at logon. Restart-on-failure retries every minute; the gateway waits
// briefly at logon because it exits when the broker is not yet reachable.
func (h Host) taskXML(service Service) string {
	user := escape(h.User)
	delay := ""
	if service.Mode == GatewayMode {
		delay = "\n      <Delay>PT10S</Delay>"
	}
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Phone Terminal ` + service.Name + ` for the signed-in user.</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + user + `</UserId>` + delay + `
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + user + `</UserId>
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
      <Arguments>` + escape("--headless "+windowsArg(h.Executable)+" "+service.Mode) + `</Arguments>
    </Exec>
  </Actions>
</Task>
`
}

// windowsArg quotes one argument for CommandLineToArgvW. Validate rejects
// quotes; a trailing backslash is doubled so it cannot escape the quote.
func windowsArg(value string) string {
	if !strings.ContainsAny(value, " \t") {
		return value
	}
	if strings.HasSuffix(value, `\`) {
		value += `\`
	}
	return `"` + value + `"`
}

// utf16File encodes Task Scheduler XML as UTF-16LE with a byte-order mark,
// matching the encoding the document declares.
func utf16File(text string) []byte {
	units := utf16.Encode([]rune(text))
	data := make([]byte, 2, 2+2*len(units))
	data[0], data[1] = 0xff, 0xfe
	for _, unit := range units {
		data = append(data, byte(unit), byte(unit>>8))
	}
	return data
}

// Status is the observed registration of one service.
type Status struct {
	Service     Service
	File        string
	FileExists  bool
	FileMatches bool
	Registered  bool
}

// Runner executes one external command with an argument array.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

// queryCommand is read-only: launchctl print / schtasks /Query.
func (h Host) queryCommand(service Service) []string {
	if h.OS == "windows" {
		return []string{"schtasks", "/Query", "/TN", h.TaskName(service)}
	}
	return []string{"launchctl", "print", h.domain() + "/" + service.Label}
}

// StartCommand asks the supervisor to start an already registered service
// without restarting a running instance.
func (h Host) StartCommand(service Service) []string {
	if h.OS == "windows" {
		return []string{"schtasks", "/Run", "/TN", h.TaskName(service)}
	}
	return []string{"launchctl", "kickstart", h.domain() + "/" + service.Label}
}

// Inspect reads registration files and queries the supervisor. It is
// read-only and must run on h.OS.
func (h Host) Inspect(ctx context.Context, runner Runner) ([]Status, error) {
	statuses := make([]Status, 0, len(Services))
	for _, service := range Services {
		want, _, err := h.Content(service)
		if err != nil {
			return nil, err
		}
		status := Status{Service: service, File: h.File(service)}
		existing, err := os.ReadFile(status.File)
		switch {
		case err == nil:
			status.FileExists = true
			status.FileMatches = string(existing) == string(want)
		case !errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("lifecycle: read %s: %w", status.File, err)
		}
		query := h.queryCommand(service)
		_, err = runner.Run(ctx, query[0], query[1:]...)
		status.Registered = err == nil
		statuses = append(statuses, status)
	}
	return statuses, nil
}

// Step is one change. Exactly one of Mkdir, Write, Remove, RemoveTree or
// Command is set.
type Step struct {
	Mkdir        string // create a current-user-only directory
	Write        string // write Data to this path
	Data         []byte
	Text         string // readable form of Data for dry runs
	Remove       string // remove one file or empty directory if present
	RemoveTree   string // remove a whole "Phone Terminal" data directory
	Command      []string
	AllowFailure bool
}

type Plan struct {
	Steps []Step
	// ChangesRunningBroker is set when an existing broker registration is
	// replaced or removed, which can stop a live broker and its sessions.
	ChangesRunningBroker bool
}

func (h Host) InstallPlan(statuses []Status) (Plan, error) {
	if err := h.Validate(); err != nil {
		return Plan{}, err
	}
	var plan Plan
	if h.OS == "windows" {
		plan.Steps = append(plan.Steps, Step{Mkdir: h.startupDir()})
	} else {
		plan.Steps = append(plan.Steps, Step{Mkdir: h.LogDir()})
	}
	for _, status := range statuses {
		if status.FileMatches && status.Registered {
			continue
		}
		service := status.Service
		if !status.FileMatches {
			data, text, err := h.Content(service)
			if err != nil {
				return Plan{}, err
			}
			plan.Steps = append(plan.Steps, Step{Write: status.File, Data: data, Text: text})
			if status.Registered && service.Mode == BrokerMode {
				plan.ChangesRunningBroker = true
			}
		}
		if h.OS == "windows" {
			// /Create /F replaces the definition; /Run is ignored for a running
			// instance because of MultipleInstancesPolicy IgnoreNew.
			name := h.TaskName(service)
			plan.Steps = append(plan.Steps,
				Step{Command: []string{"schtasks", "/Create", "/TN", name, "/XML", status.File, "/F"}},
				Step{Command: []string{"schtasks", "/Run", "/TN", name}})
			continue
		}
		if status.Registered {
			plan.Steps = append(plan.Steps, Step{Command: []string{"launchctl", "bootout", h.domain() + "/" + service.Label}})
		}
		plan.Steps = append(plan.Steps, Step{Command: []string{"launchctl", "bootstrap", h.domain(), status.File}})
	}
	if len(plan.Steps) == 1 {
		return Plan{}, nil // Everything is already registered and current.
	}
	return plan, nil
}

// UninstallPlan stops and unregisters the gateway, then the broker, and
// removes their registration files. deleteData lists data directories to
// remove afterwards; each must be named "Phone Terminal".
func (h Host) UninstallPlan(statuses []Status, deleteData []string) (Plan, error) {
	if err := h.Validate(); err != nil {
		return Plan{}, err
	}
	var plan Plan
	for index := len(statuses) - 1; index >= 0; index-- {
		status := statuses[index]
		if status.Registered {
			if status.Service.Mode == BrokerMode {
				plan.ChangesRunningBroker = true
			}
			if h.OS == "windows" {
				name := h.TaskName(status.Service)
				plan.Steps = append(plan.Steps,
					Step{Command: []string{"schtasks", "/End", "/TN", name}, AllowFailure: true},
					Step{Command: []string{"schtasks", "/Delete", "/TN", name, "/F"}})
			} else {
				plan.Steps = append(plan.Steps, Step{Command: []string{"launchctl", "bootout", h.domain() + "/" + status.Service.Label}})
			}
		}
		if status.FileExists {
			plan.Steps = append(plan.Steps, Step{Remove: status.File})
		}
	}
	if h.OS == "windows" && len(plan.Steps) > 0 {
		plan.Steps = append(plan.Steps, Step{Remove: h.startupDir(), AllowFailure: true})
	}
	for _, path := range deleteData {
		if err := h.checkPath("data", path); err != nil {
			return Plan{}, err
		}
		base := path[strings.LastIndexAny(path, `/\`)+1:]
		if base != "Phone Terminal" {
			return Plan{}, fmt.Errorf("lifecycle: refusing to delete %q: not a Phone Terminal data directory", path)
		}
		plan.Steps = append(plan.Steps, Step{RemoveTree: path})
	}
	return plan, nil
}

// Describe writes each step exactly as Apply would perform it.
func Describe(plan Plan, output io.Writer) error {
	for _, step := range plan.Steps {
		if _, err := fmt.Fprintln(output, "  "+step.String()); err != nil {
			return err
		}
		if step.Write != "" {
			if _, err := fmt.Fprint(output, indent(step.Text)); err != nil {
				return err
			}
		}
	}
	return nil
}

func indent(text string) string {
	lines := strings.SplitAfter(strings.TrimSuffix(text, "\n"), "\n")
	return "      | " + strings.Join(lines, "      | ") + "\n"
}

func (step Step) String() string {
	switch {
	case step.Mkdir != "":
		return "create private directory " + step.Mkdir
	case step.Write != "":
		return "write " + step.Write + ":"
	case step.Remove != "":
		return "remove " + step.Remove
	case step.RemoveTree != "":
		return "delete all data in " + step.RemoveTree
	default:
		quoted := make([]string, len(step.Command))
		for index, argument := range step.Command {
			quoted[index] = displayArg(argument)
		}
		return "run " + strings.Join(quoted, " ")
	}
}

func displayArg(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(`_-./\:=@%+,`, r))
	}) < 0 {
		return value
	}
	if strings.ContainsRune(value, '"') {
		return strconv.Quote(value)
	}
	return `"` + value + `"`
}

// Apply performs plan on the current OS, stopping at the first failure that
// is not explicitly allowed.
func Apply(ctx context.Context, runner Runner, plan Plan, output io.Writer) error {
	for _, step := range plan.Steps {
		if _, err := fmt.Fprintln(output, "  "+step.String()); err != nil {
			return err
		}
		var err error
		switch {
		case step.Mkdir != "":
			_, err = ipc.EnsurePrivateDirectory(step.Mkdir)
		case step.Write != "":
			if err = os.MkdirAll(filepath.Dir(step.Write), 0o755); err == nil {
				err = os.WriteFile(step.Write, step.Data, 0o644)
			}
		case step.Remove != "":
			if err = os.Remove(step.Remove); errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		case step.RemoveTree != "":
			err = os.RemoveAll(step.RemoveTree)
		default:
			var result string
			result, err = runner.Run(ctx, step.Command[0], step.Command[1:]...)
			if err != nil {
				err = fmt.Errorf("%w: %s", err, bounded(result))
			}
		}
		if err != nil && !step.AllowFailure {
			return fmt.Errorf("lifecycle: %s: %w", step.String(), err)
		}
	}
	return nil
}

func bounded(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 512 {
		text = text[:512] + "..."
	}
	return text
}
