package herdr

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	availabilityEvery   = 30 * time.Second
	maxAgentPathEntries = 64
	maxAgentPathBytes   = 4096
	maxStartableKinds   = 64
)

// Canonical Unix commands used by herdr's agent.start, not detection aliases.
// Source: herdr v0.9.3 src/detect/mod.rs interactive_agent_executable.
// Unknown future kinds still appear when herdr observes them running; an
// arbitrary advertised kind is never treated as a filesystem command.
var agentExecutables = map[string]string{
	"pi": "pi", "claude": "claude", "codex": "codex", "gemini": "gemini",
	"cursor": "cursor-agent", "devin": "devin", "agy": "agy", "cline": "cline",
	"omp": "omp", "mastracode": "mastracode", "opencode": "opencode", "copilot": "copilot",
	"kimi": "kimi", "kiro": "kiro-cli", "droid": "droid", "amp": "amp", "grok": "grok",
	"hermes": "hermes", "kilo": "kilo", "qodercli": "qodercli", "qwen": "qwen",
	"letta": "letta", "maki": "maki", "muse": "muse",
}

// refreshAvailability belongs to the poll loop. Settings reads only the
// published kinds and asks RefreshAgents for a refresh; it never walks PATH.
func (provider *Provider) refreshAvailability(now time.Time) {
	if now.Before(provider.availabilityAt) {
		return
	}
	provider.available = installedKinds(provider.caps.kinds, provider.config.PathEnv)
	provider.availabilityAt = now.Add(availabilityEvery)
}

// installedKinds checks at most 24 known commands in the first 64 PATH
// entries, using only regular executable files in absolute directories.
// It never executes a command, reads agent settings or starts a shell.
// Presence is a hint: herdr's pane shell may have a different PATH, aliases
// or login settings; agent.start remains the authority for an actual launch.
func installedKinds(startable []string, pathEnv string) map[string]bool {
	available := make(map[string]bool)
	var folders []string
	entries := 0
	for folder := range strings.SplitSeq(pathEnv, string(os.PathListSeparator)) {
		entries++
		if entries > maxAgentPathEntries {
			break
		}
		if len(folder) <= maxAgentPathBytes && filepath.IsAbs(folder) {
			folders = append(folders, folder)
		}
	}
	for _, kind := range startable {
		command, known := agentExecutables[kind]
		if !known || available[kind] {
			continue
		}
		for _, folder := range folders {
			info, err := os.Stat(filepath.Join(folder, command))
			if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				available[kind] = true
				break
			}
		}
	}
	return available
}
