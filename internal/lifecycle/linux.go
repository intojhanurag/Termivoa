package lifecycle

import (
	"context"
	"strings"
)

// SystemdUser reports whether a systemd user manager answers for this user.
// `systemctl --user is-system-running` is read-only; it exits non-zero for
// "degraded" (one failed unit anywhere), which still accepts units. Without a
// manager (WSL without systemd, containers) it prints "offline" or fails to
// connect; reason then says why.
func SystemdUser(ctx context.Context, runner Runner) (available bool, reason string) {
	output, err := runner.Run(ctx, "systemctl", "--user", "is-system-running")
	state := strings.TrimSpace(output)
	switch state {
	case "initializing", "starting", "running", "degraded", "maintenance":
		return true, ""
	}
	if state == "" && err != nil {
		state = err.Error()
	}
	return false, bounded(strings.ReplaceAll(state, "\n", " "))
}

// LingerQuery reads whether systemd keeps the user's services running
// without a login (read-only).
func (h Host) LingerQuery() []string {
	return []string{"loginctl", "show-user", h.User, "--property=Linger", "--value"}
}

// LingerCommand keeps the user's services running after logout and starts
// them at boot. pterm runs it only after its own explicit yes, never with sudo.
func (h Host) LingerCommand() []string { return []string{"loginctl", "enable-linger", h.User} }

// Linger reports the linger setting; known is false when loginctl did not
// answer (no systemd-logind).
func (h Host) Linger(ctx context.Context, runner Runner) (enabled, known bool) {
	query := h.LingerQuery()
	output, err := runner.Run(ctx, query[0], query[1:]...)
	switch strings.TrimSpace(output) {
	case "yes":
		return true, err == nil
	case "no":
		return false, err == nil
	}
	return false, false
}
