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
			problems = append(problems, "pterm is running from a mounted disk image; copy Phone Terminal.app to Applications first")
		}
		if strings.Contains(path, "/AppTranslocation/") {
			problems = append(problems, "macOS App Translocation is active; move Phone Terminal.app to Applications and open it from there")
		}
	}
	if goos == "windows" && strings.Contains(lower, "/appdata/local/temp/") {
		problems = append(problems, "pterm is running from a temporary directory; install it first")
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

// ServeFindings summarizes `tailscale serve status --json`. configured is
// true when HTTPS 443 already proxies the local gateway; problems name
// conflicts that pterm must not overwrite and any Funnel exposure.
func ServeFindings(data []byte) (configured bool, problems []string, err error) {
	var raw struct {
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
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, nil, nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, nil, fmt.Errorf("parse tailscale serve status: %w", err)
	}
	for _, hostPort := range sortedKeys(raw.AllowFunnel) {
		if raw.AllowFunnel[hostPort] {
			problems = append(problems, "Tailscale Funnel is enabled for "+hostPort+"; Phone Terminal must never be public. Turn Funnel off before pairing.")
		}
	}
	if forward := raw.TCP["443"].TCPForward; forward != "" {
		problems = append(problems, "port 443 already forwards TCP to "+forward+"; pterm will not overwrite it")
	}
	for _, hostPort := range sortedKeys(raw.Web) {
		if !strings.HasSuffix(hostPort, ":443") {
			continue
		}
		handlers := raw.Web[hostPort].Handlers
		for _, path := range sortedKeys(handlers) {
			handler := handlers[path]
			if path == "/" && strings.TrimSuffix(handler.Proxy, "/") == "http://127.0.0.1:8787" {
				configured = true
				continue
			}
			target := handler.Proxy
			if target == "" {
				target = "a static handler"
			}
			problems = append(problems, "HTTPS 443 path "+path+" on "+hostPort+" already serves "+target+"; pterm will not overwrite it")
		}
	}
	return configured, problems, nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
