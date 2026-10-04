package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// herdrConfig is the production herdr.Config (docs/herdr-provider.md,
// section 3): herdr's install paths first, then PATH, and the user's home.
// Nil on Windows and without a home: no provider.
func herdrConfig() *herdr.Config {
	home, err := os.UserHomeDir()
	if runtime.GOOS == "windows" || err != nil || !filepath.IsAbs(home) {
		return nil
	}
	paths := []string{"/usr/local/bin/herdr", "/usr/bin/herdr"}
	if runtime.GOOS == "darwin" {
		paths = []string{"/opt/homebrew/bin/herdr", "/usr/local/bin/herdr"}
	}
	paths = append(paths, filepath.Join(home, ".local", "bin", "herdr"), filepath.Join(home, ".cargo", "bin", "herdr"))
	return &herdr.Config{BinaryPaths: paths, PathEnv: os.Getenv("PATH"), Home: home}
}

// inspectHerdr prints the herdr line of `pterm doctor`, only when herdr is
// installed (nothing changes on a computer without it). It never counts a
// problem: herdr is optional.
func inspectHerdr(ctx context.Context, config *herdr.Config, output io.Writer) {
	if config == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status := herdr.Inspect(ctx, *config)
	for _, reason := range status.Refused {
		fmt.Fprintf(output, "  ! %s\n", reason)
	}
	if status.Binary == "" {
		return
	}
	switch status.State {
	case protocol.HTTPProviderStateReady:
		create := "cannot make worktrees"
		if status.CanCreate {
			create = "can make worktrees"
		}
		fmt.Fprintf(output, "  herdr: %s %s (protocol %d), server running at %s, %s\n",
			status.Binary, status.Version, status.Protocol, status.Socket, create)
	case protocol.Unsupported:
		fmt.Fprintf(output, "  ! herdr: %s %s (protocol %d) is too old or unreadable; update herdr (0.8.2 or later)\n",
			status.Binary, status.Version, status.Protocol)
	default:
		fmt.Fprintf(output, "  herdr: %s, server not running (%s)\n", status.Binary, status.Socket)
	}
	if status.CustomPrefix {
		fmt.Fprintln(output, "  ! herdr: config.toml sets a prefix key other than ctrl+b; Termivoa doubles only ctrl+b, "+
			"so a phone can send that prefix to a herdr terminal it opened (at worst it detaches the phone)")
	}
	if len(status.AgentKinds) > 0 {
		fmt.Fprintf(output, "  herdr agents the phone can start: %s\n", strings.Join(status.AgentKinds, ", "))
	}
}
