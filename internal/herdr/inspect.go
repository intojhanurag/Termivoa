package herdr

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Status is what `pterm doctor` reports about herdr: one read-only pass of
// the poll loop (ping, probes, one snapshot) without the broker.
type Status struct {
	Binary  string   // "" when herdr is not installed
	Refused []string // why herdr binaries were not used
	Socket  string
	// SocketRefused is why the socket was not used, with the fix ("" when it
	// was used or is missing).
	SocketRefused string
	State         protocol.HTTPProviderState
	Version       string
	Protocol      int
	CanCreate     bool
	AttachAny     bool
	AgentKinds    []string
	// CustomPrefix: config.toml sets a prefix other than ctrl+b, which the
	// broker does not double (design Q1).
	CustomPrefix bool
}

// Inspect reads herdr once.
func Inspect(ctx context.Context, config Config) Status {
	provider := New(config, nil)
	provider.logf = func(string, ...any) {}
	provider.step(ctx)
	_, refused := FindBinary(config)
	info, _ := provider.Info()
	status := Status{
		Binary: provider.binary, Refused: refused, Socket: config.socketPath(), State: info.State,
		Version: provider.view.version, Protocol: provider.view.protocol, CanCreate: info.CanCreate,
		AttachAny: provider.view.caps.attachAny, AgentKinds: info.AgentKinds, SocketRefused: provider.view.refused,
	}
	if status.Binary != "" {
		status.CustomPrefix = customPrefix(filepath.Join(config.folder(), "config.toml"))
	}
	return status
}

var inlinePrefix = regexp.MustCompile(`(?:^|[{,])\s*prefix\s*=\s*("[^"]*"|'[^']*')`)

// customPrefix reports whether config.toml sets the keys prefix ([keys]
// prefix, keys.prefix, or keys = { prefix = … }) to anything other than
// "ctrl+b". It reads the file only.
func customPrefix(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	section := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[] ")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if ok && section == "" && key == "keys" {
			if found := inlinePrefix.FindStringSubmatch(strings.TrimSpace(value)); found != nil {
				return strings.Trim(found[1], `"'`) != "ctrl+b"
			}
			continue
		}
		if !ok || !(section == "keys" && key == "prefix") && !(section == "" && key == "keys.prefix") {
			continue
		}
		value, _, _ = strings.Cut(value, "#")
		return strings.Trim(strings.TrimSpace(value), `"'`) != "ctrl+b"
	}
	return false
}
