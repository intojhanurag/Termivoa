package lifecycle

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ExecutableProblems reports why a binary location must not be registered
// for startup: it would disappear or move after this run.
func ExecutableProblems(goos, path string) []string {
	var problems []string
	lower := strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	if strings.Contains(lower, "/go-build") {
		problems = append(problems, "pterm is a temporary `go run` build; build or install it first")
	}
	if goos == "darwin" {
		if strings.HasPrefix(path, "/Volumes/") {
			problems = append(problems, "pterm is running from a mounted disk image; copy Termivoa.app to Applications first")
		}
		if strings.Contains(path, "/AppTranslocation/") {
			problems = append(problems, "macOS App Translocation is active; move Termivoa.app to Applications and open it from there")
		}
	}
	if goos == "windows" && strings.Contains(lower, "/appdata/local/temp/") {
		problems = append(problems, "pterm is running from a temporary directory; install it first")
	}
	if goos == "linux" && (strings.HasPrefix(path, "/tmp/") || strings.HasPrefix(path, "/var/tmp/") || strings.HasPrefix(path, "/dev/shm/")) {
		problems = append(problems, "pterm is running from a temporary directory; install it first (scripts/install-linux.sh puts it in ~/.local/bin)")
	}
	return problems
}

// ServeStatus summarizes `tailscale serve status --json` for this computer's
// Tailscale DNS name. Configured is true when HTTPS 443 on that name already
// proxies the local gateway in the saved (--bg) config. A handler under
// another name (left from an old machine name) is another origin: it is
// neither Configured nor a problem. Problems name conflicts on 443 that pterm
// must not overwrite, Termivoa served by a Tailscale Service, and any Funnel
// exposure (checked once, not per port: Funnel anywhere is already a
// problem). Funnel lists the host:port of Termivoa's own ports (443, 8443),
// or of any port that proxies Termivoa, with Funnel on: setup refuses them
// even when Serve already proxies Termivoa there. ForegroundFunnel is the same
// for `tailscale serve` or `tailscale funnel` commands that run in the
// foreground (without --bg); they never count as Configured, and stopping
// them is the fix. PreviewConfigured and PreviewProblems report the same for
// the separate HTTPS 8443 dev-server preview listener; a preview conflict
// never blocks the 443 flow, and a 443 conflict never blocks the preview flow.
type ServeStatus struct {
	Configured        bool
	Problems          []string
	Funnel            []string
	ForegroundFunnel  []string
	PreviewConfigured bool
	PreviewProblems   []string
}

// ForegroundStop is how the owner ends a foreground Serve or Funnel: a config
// that lives only while its command runs. pterm never stops it.
const ForegroundStop = "stop the `tailscale serve` or `tailscale funnel` command that runs in the foreground (Ctrl+C)"

// serveConfig is the part of Tailscale's ipn.ServeConfig that pterm reads;
// `tailscale serve status --json` prints the whole ServeConfig. Foreground
// holds one config per foreground command, keyed by its session ID. Services
// holds Tailscale Services (ipn.ServiceConfig has only TCP and Web: Tailscale
// has no Funnel for Services).
type serveConfig struct {
	TCP map[string]struct {
		TCPForward string
	}
	Web map[string]struct {
		Handlers map[string]serveHandler
	}
	AllowFunnel map[string]bool
	Foreground  map[string]serveConfig
	Services    map[string]serveConfig
}

type serveHandler struct{ Proxy, Path, Text string }

// ServeFindings reads `tailscale serve status --json`. host is this
// computer's Tailscale DNS name (tailnet.Node.Host); only <host>:443 and
// <host>:8443 count as configured. Funnel does not depend on host.
func ServeFindings(data []byte, host string) (ServeStatus, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return ServeStatus{}, nil
	}
	var raw serveConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return ServeStatus{}, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	var status ServeStatus
	status.read(raw, host, false)
	for _, session := range slices.Sorted(maps.Keys(raw.Foreground)) {
		status.read(raw.Foreground[session], host, true)
	}
	return status, nil
}

// read adds the findings of one config: the saved one, or a foreground one.
func (status *ServeStatus) read(config serveConfig, host string, foreground bool) {
	funnel, fix := &status.Funnel, func(hostPort string) string { return "Turn it off: `" + FunnelOffCommand(hostPort) + "`" }
	if foreground {
		funnel, fix = &status.ForegroundFunnel, func(string) string { return "Turn it off: " + ForegroundStop }
	}
	for _, hostPort := range slices.Sorted(maps.Keys(config.AllowFunnel)) {
		if !config.AllowFunnel[hostPort] {
			continue
		}
		status.Problems = append(status.Problems, "Tailscale Funnel is on for "+hostPort+", which makes it public on the internet; Termivoa must never be public. "+
			fix(hostPort)+", then run `pterm setup` again.")
		termivoa := strings.HasSuffix(hostPort, ":443") || strings.HasSuffix(hostPort, ":8443") || servesTermivoa(config.Web[hostPort].Handlers)
		if termivoa && !slices.Contains(*funnel, hostPort) {
			*funnel = append(*funnel, hostPort)
		}
	}
	configured, problems := servePortFindings(config, host, "443", LocalURL, foreground)
	status.Configured = status.Configured || configured
	status.Problems = append(status.Problems, problems...)
	configured, problems = servePortFindings(config, host, "8443", PreviewLocalURL, foreground)
	status.PreviewConfigured = status.PreviewConfigured || configured
	status.PreviewProblems = append(status.PreviewProblems, problems...)
	// A Service is its own name, which other computers may serve too;
	// Termivoa belongs only on this computer's own name.
	for _, name := range slices.Sorted(maps.Keys(config.Services)) {
		web := config.Services[name].Web
		for _, hostPort := range slices.Sorted(maps.Keys(web)) {
			if servesTermivoa(web[hostPort].Handlers) {
				status.Problems = append(status.Problems, "the Tailscale Service "+name+" serves Termivoa at "+hostPort+
					"; serve Termivoa only at this computer's own name. Remove it: `tailscale serve clear "+name+"`")
			}
		}
	}
}

// servesTermivoa reports whether a handler proxies Termivoa's gateway or
// preview listener: Funnel on any port (10000 too) then makes it public.
func servesTermivoa(handlers map[string]serveHandler) bool {
	for _, handler := range handlers {
		if target := strings.TrimSuffix(handler.Proxy, "/"); target == strings.TrimSuffix(LocalURL, "/") || target == strings.TrimSuffix(PreviewLocalURL, "/") {
			return true
		}
	}
	return false
}

// FunnelOffCommand turns Funnel off for hostPort ("host:443"). It also
// removes the Serve handler on that port; `pterm setup` then asks again to
// share Termivoa there. pterm never runs it (Tailscale changes are the owner's).
// It does not stop a foreground Funnel (ForegroundStop does).
func FunnelOffCommand(hostPort string) string {
	return "tailscale funnel --https=" + hostPort[strings.LastIndexByte(hostPort, ':')+1:] + " off"
}

// servePortFindings reports whether target ("http://127.0.0.1:<port>/") is
// already proxied at HTTPS port on host in the saved config, and names any
// TCP forward or other handler on that port that pterm must not overwrite.
// Termivoa under another host name is skipped: it is another origin. In a
// foreground config every handler is a problem, Termivoa too: it ends with
// its command.
func servePortFindings(config serveConfig, host, port, target string, foreground bool) (configured bool, problems []string) {
	free := "pterm will not overwrite it"
	if foreground {
		free = "to free it, " + ForegroundStop
	}
	if forward := config.TCP[port].TCPForward; forward != "" {
		problems = append(problems, "port "+port+" already forwards TCP to "+forward+"; "+free)
	}
	suffix := ":" + port
	for _, hostPort := range slices.Sorted(maps.Keys(config.Web)) {
		if !strings.HasSuffix(hostPort, suffix) {
			continue
		}
		handlers := config.Web[hostPort].Handlers
		for _, path := range slices.Sorted(maps.Keys(handlers)) {
			handler := handlers[path]
			if !foreground && path == "/" && strings.TrimSuffix(handler.Proxy, "/") == strings.TrimSuffix(target, "/") {
				configured = configured || strings.EqualFold(strings.TrimSuffix(hostPort, suffix), host)
				continue
			}
			handlerTarget := handler.Proxy
			if handlerTarget == "" {
				handlerTarget = "a static handler"
			}
			problems = append(problems, "HTTPS "+port+" path "+path+" on "+hostPort+" already serves "+handlerTarget+"; "+free)
		}
	}
	return configured, problems
}

// TailscaleOperatorCommand lets the current Linux user change Tailscale
// Serve without root. The owner runs it once; pterm never runs sudo.
const TailscaleOperatorCommand = "sudo tailscale set --operator=$USER"

// TailscaleOperator returns OperatorUser from `tailscale debug prefs`, which
// any local user may read. Empty means no operator is set.
func TailscaleOperator(prefs []byte) (string, error) {
	var raw struct{ OperatorUser string }
	if err := json.Unmarshal(prefs, &raw); err != nil {
		return "", fmt.Errorf("parse tailscale prefs: %w", err)
	}
	return raw.OperatorUser, nil
}

// NeedsTailscaleOperator reports whether `tailscale serve` output is the
// Linux permission error: only root or the operator may change Serve.
// tailscale prints "Access denied: serve config denied" and suggests
// `sudo tailscale set --operator=$USER`.
func NeedsTailscaleOperator(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "access denied") || strings.Contains(lower, "--operator")
}
