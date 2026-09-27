package lifecycle

import (
	"encoding/json"
	"fmt"
	"sort"
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

// TailscaleStatus is the non-secret subset of `tailscale status --json`.
type TailscaleStatus struct {
	BackendState string
	DNSName      string
}

func ParseTailscaleStatus(data []byte) (TailscaleStatus, error) {
	var raw struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return TailscaleStatus{}, fmt.Errorf("parse tailscale status: %w", err)
	}
	return TailscaleStatus{BackendState: raw.BackendState, DNSName: strings.TrimSuffix(raw.Self.DNSName, ".")}, nil
}

// ServeStatus summarizes `tailscale serve status --json`. Configured is true
// when HTTPS 443 already proxies the local gateway; Problems name conflicts
// on 443 that pterm must not overwrite and any Funnel exposure (checked once,
// not per port: Funnel anywhere is already a problem). PreviewConfigured and
// PreviewProblems report the same for the separate HTTPS 8443 dev-server
// preview listener; a preview conflict never blocks the 443 flow, and a 443
// conflict never blocks the preview flow.
type ServeStatus struct {
	Configured        bool
	Problems          []string
	PreviewConfigured bool
	PreviewProblems   []string
}

type rawServeStatus struct {
	TCP map[string]struct {
		TCPForward string
	}
	Web map[string]struct {
		Handlers map[string]struct {
			Proxy, Path, Text string
		}
	}
	AllowFunnel map[string]bool
}

func ServeFindings(data []byte) (ServeStatus, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return ServeStatus{}, nil
	}
	var raw rawServeStatus
	if err := json.Unmarshal(data, &raw); err != nil {
		return ServeStatus{}, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	var status ServeStatus
	for _, hostPort := range sortedKeys(raw.AllowFunnel) {
		if raw.AllowFunnel[hostPort] {
			status.Problems = append(status.Problems, "Tailscale Funnel is enabled for "+hostPort+"; Termivoa must never be public. Turn Funnel off before pairing.")
		}
	}
	configured, problems := servePortFindings(raw, "443", LocalURL)
	status.Configured = configured
	status.Problems = append(status.Problems, problems...)
	status.PreviewConfigured, status.PreviewProblems = servePortFindings(raw, "8443", PreviewLocalURL)
	return status, nil
}

// servePortFindings reports whether target ("http://127.0.0.1:<port>/") is
// already proxied at HTTPS port, and names any TCP forward or other handler
// on that port that pterm must not overwrite.
func servePortFindings(raw rawServeStatus, port, target string) (configured bool, problems []string) {
	if forward := raw.TCP[port].TCPForward; forward != "" {
		problems = append(problems, "port "+port+" already forwards TCP to "+forward+"; pterm will not overwrite it")
	}
	suffix := ":" + port
	for _, hostPort := range sortedKeys(raw.Web) {
		if !strings.HasSuffix(hostPort, suffix) {
			continue
		}
		handlers := raw.Web[hostPort].Handlers
		for _, path := range sortedKeys(handlers) {
			handler := handlers[path]
			if path == "/" && strings.TrimSuffix(handler.Proxy, "/") == strings.TrimSuffix(target, "/") {
				configured = true
				continue
			}
			handlerTarget := handler.Proxy
			if handlerTarget == "" {
				handlerTarget = "a static handler"
			}
			problems = append(problems, "HTTPS "+port+" path "+path+" on "+hostPort+" already serves "+handlerTarget+"; pterm will not overwrite it")
		}
	}
	return configured, problems
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
