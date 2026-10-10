// Package lifecycle models per-user startup registration for the separately
// supervised broker and gateway processes. It generates macOS LaunchAgent
// property lists, Windows Scheduled Task XML and Linux systemd user units,
// plans install/uninstall steps and applies them only through an injected
// command runner. Nothing here requests elevation, registers a system-wide
// service, or changes Tailscale.
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

	// PreviewLocalURL is the fixed dev-server preview listener loopback
	// address, a second and separate origin from LocalURL.
	PreviewLocalURL = "http://127.0.0.1:8789/"
	// PreviewServeCommand is printed for the owner to run, or offered as a
	// second, separate consent after ServeCommand. pterm never runs it
	// without that explicit yes.
	PreviewServeCommand = "tailscale serve --bg --https=8443 http://127.0.0.1:8789"
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
	OS         string // "darwin", "windows" or "linux"
	Executable string // absolute installed pterm path
	DataDir    string // production data dir: logs (darwin), task XML (windows)
	HomeDir    string // darwin: parent of Library/LaunchAgents
	UID        int    // darwin: gui/<uid> launchd domain
	User       string // windows: DOMAIN\user owning the tasks; linux: login name
	// Logs overrides <DataDir>/logs; Linux keeps logs in $XDG_STATE_HOME.
	Logs string
	// ConfigDir is $XDG_CONFIG_HOME (linux); units go in <ConfigDir>/systemd/user.
	ConfigDir string
	// Systemd reports whether a systemd user manager answers (linux). Without
	// one nothing is registered: WSL without systemd, containers.
	Systemd bool
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
	case "linux":
		if h.User == "" || h.User[0] == '-' || strings.ContainsAny(h.User, "/:\\ \t\n\x00") {
			return errors.New("lifecycle: Linux user name is not usable")
		}
		for kind, path := range map[string]string{"home": h.HomeDir, "config": h.ConfigDir, "log": h.Logs} {
			if err := h.checkPath(kind, path); err != nil {
				return err
			}
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
	// systemd unit files would need quoting or %/$ escaping for these; an
	// installed pterm and XDG directories never contain them.
	if h.OS == "linux" && strings.ContainsAny(path, " \t\"'\\%$;") {
		return fmt.Errorf("lifecycle: %s path %q must not contain spaces, quotes, backslashes, %%, $ or ;", kind, path)
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

// LogDir holds launchd or systemd stdout/stderr and detached-start logs.
func (h Host) LogDir() string {
	if h.Logs != "" {
		return h.Logs
	}
	return h.join(h.DataDir, "logs")
}

func (h Host) startupDir() string { return h.join(h.DataDir, "startup") }

// File is the registration source file for a service.
func (h Host) File(service Service) string {
	switch h.OS {
	case "windows":
		return h.join(h.startupDir(), service.Name+".xml")
	case "linux":
		return h.join(h.ConfigDir, "systemd", "user", Unit(service))
	}
	return h.join(h.HomeDir, "Library", "LaunchAgents", service.Label+".plist")
}

// Labels, unit names, task names and the unit/task descriptions keep the old
// "Phone Terminal" working name: changing them would orphan or invalidate
// existing startup registrations (docs/naming.md).

// Unit is the systemd user unit name of a service.
func Unit(service Service) string { return "phone-terminal-" + service.Name + ".service" }

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
	switch h.OS {
	case "windows":
		text = h.taskXML(service)
		return utf16File(text), text, nil
	case "linux":
		text = h.systemdUnit(service)
		return []byte(text), text, nil
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

// systemdUnit runs pterm for this user only. It restarts on failure but not
// after a clean exit (pterm stop, pterm shutdown), like launchd's
// SuccessfulExit=false. The gateway wants the broker and starts after it, and
// itself waits for the broker. Output is appended to the same log files that
// macOS uses; `journalctl --user -u <unit>` shows start and stop events.
// Checked paths need no quoting (see checkPath).
func (h Host) systemdUnit(service Service) string {
	dependency := ""
	if service.Mode == GatewayMode {
		broker := Unit(Services[0])
		dependency = "Wants=" + broker + "\nAfter=" + broker + "\n"
	}
	log := h.join(h.LogDir(), service.Name+".log")
	return `[Unit]
Description=Phone Terminal ` + service.Name + ` for this user
` + dependency + `
[Service]
Type=simple
ExecStart=` + h.Executable + ` ` + service.Mode + `
Restart=on-failure
RestartSec=5
StandardOutput=append:` + log + `
StandardError=inherit

[Install]
WantedBy=default.target
`
}

// taskXML runs pterm through a headless console host so no console window
// appears at logon, at normal priority (5) because sessions inherit it: the
// default 7 is below normal. The gateway starts a little later and itself
// waits for the broker. pterm writes its own log because the headless console
// discards output.
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
    <Priority>5</Priority>
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
	Registered  bool // registered and enabled
	Disabled    bool // registered, but its Windows task is disabled
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

// queryCommand is read-only: launchctl print / schtasks /Query / systemctl
// is-enabled. The Windows task XML shows whether the task is disabled; a
// disabled task still exists.
func (h Host) queryCommand(service Service) []string {
	switch h.OS {
	case "windows":
		return []string{"schtasks", "/Query", "/TN", h.TaskName(service), "/XML"}
	case "linux":
		return systemctl("is-enabled", Unit(service))
	}
	return []string{"launchctl", "print", h.domain() + "/" + service.Label}
}

// StartCommand asks the supervisor to start an already registered service
// without restarting a running instance.
func (h Host) StartCommand(service Service) []string {
	switch h.OS {
	case "windows":
		return []string{"schtasks", "/Run", "/TN", h.TaskName(service)}
	case "linux":
		return systemctl("start", Unit(service))
	}
	return []string{"launchctl", "kickstart", h.domain() + "/" + service.Label}
}

// UnloadCommand stops a registered service and keeps its supervisor from
// starting it again until `pterm setup --apply` registers it again. pterm
// only prints it; the owner decides whether to run it.
func (h Host) UnloadCommand(service Service) []string {
	switch h.OS {
	case "windows":
		return []string{"schtasks", "/Change", "/TN", h.TaskName(service), "/Disable"}
	case "linux":
		return systemctl("disable", "--now", Unit(service))
	}
	return []string{"launchctl", "bootout", h.domain() + "/" + service.Label}
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
		if h.OS == "linux" && !h.Systemd {
			statuses = append(statuses, status)
			continue // Nothing can be registered without a user manager.
		}
		query := h.queryCommand(service)
		output, err := runner.Run(ctx, query[0], query[1:]...)
		// Dropping NULs also reads UTF-16 output. Only the task-level and the
		// trigger's <Enabled> exist; either false means it never starts.
		status.Disabled = err == nil && h.OS == "windows" &&
			strings.Contains(strings.ReplaceAll(output, "\x00", ""), "<Enabled>false</Enabled>")
		status.Registered = err == nil && !status.Disabled
		if h.OS == "linux" {
			// is-enabled prints the state and exits non-zero unless enabled.
			state := strings.TrimSpace(output)
			status.Registered = err == nil && state == "enabled"
			status.Disabled = state == "disabled"
		}
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
	KeepDataLock bool   // for offline cleanup, preserve the private directory/lock inode
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
	if h.OS == "linux" {
		return h.systemdInstallPlan(statuses)
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
		}
		// Recreating a disabled task may end the instance it still runs.
		if (status.Registered && !status.FileMatches || status.Disabled) && service.Mode == BrokerMode {
			plan.ChangesRunningBroker = true
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

// systemdInstallPlan writes changed units, reloads systemd once, enables each
// unit for login (and boot, with linger), and starts it. A changed unit is
// restarted so the new pterm runs; for the broker that ends its sessions, so
// the caller refuses while sessions are active (ChangesRunningBroker).
func (h Host) systemdInstallPlan(statuses []Status) (Plan, error) {
	if !h.Systemd {
		return Plan{}, errors.New("lifecycle: systemd user services are not available")
	}
	plan := Plan{Steps: []Step{{Mkdir: h.LogDir()}}}
	var commands []Step
	for _, status := range statuses {
		if status.FileMatches && status.Registered {
			continue
		}
		unit := Unit(status.Service)
		start := "start"
		if !status.FileMatches {
			data, text, err := h.Content(status.Service)
			if err != nil {
				return Plan{}, err
			}
			plan.Steps = append(plan.Steps, Step{Write: status.File, Data: data, Text: text})
			start = "restart"
			if (status.Registered || status.Disabled) && status.Service.Mode == BrokerMode {
				plan.ChangesRunningBroker = true
			}
		}
		if !status.Registered {
			commands = append(commands, Step{Command: systemctl("enable", unit)})
		}
		commands = append(commands, Step{Command: systemctl(start, unit)})
	}
	if len(commands) == 0 {
		return Plan{}, nil // Everything is already registered and current.
	}
	if len(plan.Steps) > 1 {
		plan.Steps = append(plan.Steps, Step{Command: systemctl("daemon-reload")})
	}
	plan.Steps = append(plan.Steps, commands...)
	return plan, nil
}

func systemctl(args ...string) []string { return append([]string{"systemctl", "--user"}, args...) }

// UninstallPlan stops and unregisters the gateway, then the broker, and
// removes their registration files. deleteData lists data directories to
// remove afterwards; each must be named "Phone Terminal" (or "phone-terminal"
// on Linux).
func (h Host) UninstallPlan(statuses []Status, deleteData []string) (Plan, error) {
	if err := h.Validate(); err != nil {
		return Plan{}, err
	}
	var plan Plan
	for index := len(statuses) - 1; index >= 0; index-- {
		status := statuses[index]
		if status.Registered || status.Disabled {
			if status.Service.Mode == BrokerMode {
				plan.ChangesRunningBroker = true
			}
			switch h.OS {
			case "linux":
				plan.Steps = append(plan.Steps, Step{Command: systemctl("disable", "--now", Unit(status.Service))})
			case "windows":
				name := h.TaskName(status.Service)
				plan.Steps = append(plan.Steps,
					Step{Command: []string{"schtasks", "/End", "/TN", name}, AllowFailure: true},
					Step{Command: []string{"schtasks", "/Delete", "/TN", name, "/F"}})
			default:
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
	if h.OS == "linux" && h.Systemd && len(plan.Steps) > 0 {
		plan.Steps = append(plan.Steps, Step{Command: systemctl("daemon-reload")})
	}
	for _, path := range deleteData {
		if err := h.checkPath("data", path); err != nil {
			return Plan{}, err
		}
		base := path[strings.LastIndexAny(path, `/\`)+1:]
		if base != "Phone Terminal" && base != "phone-terminal" {
			return Plan{}, fmt.Errorf("lifecycle: refusing to delete %q: not a Termivoa data directory", path)
		}
		plan.Steps = append(plan.Steps, Step{RemoveTree: path, KeepDataLock: path == h.DataDir && (h.OS == "darwin" || h.OS == "linux")})
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
		if step.KeepDataLock {
			return "delete saved data in " + step.RemoveTree + " (keep the empty private ownership directory)"
		}
		return "delete all data in " + step.RemoveTree
	default:
		return "run " + DisplayCommand(step.Command)
	}
}

// DisplayCommand quotes a command line for the owner to copy.
func DisplayCommand(command []string) string {
	quoted := make([]string, len(command))
	for index, argument := range command {
		quoted[index] = displayArg(argument)
	}
	return strings.Join(quoted, " ")
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
			if step.KeepDataLock {
				err = removeDataContents(ctx, step.RemoveTree)
			} else {
				err = os.RemoveAll(step.RemoveTree)
			}
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

// removeDataContents keeps the lock inode and its parent directory reachable.
// Removing either would let a restarted broker take a replacement lock before
// this cleanup finishes. The CLI holds the broker data lock throughout.
func removeDataContents(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ipc.BrokerDataLockFile {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
