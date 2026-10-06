package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/github"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// githubCredentialMode is the hidden command git runs as its credential
// helper in a folder that Termivoa cloned (docs/github-integration.md, 6.6).
const githubCredentialMode = "github-credential"

const (
	gitCredentialMaxBytes = 64 << 10
	gitCredentialMaxLines = 32
	githubNotConnected    = "Termivoa is not connected to GitHub on this computer. Connect in Settings → Integrations."
	githubRevokeHint      = "To be sure the login is removed on GitHub too, open https://github.com/settings/applications"
)

// githubConfig is the production github.Config: git's install paths first,
// then PATH, the user's home and this pterm's resolved path. Nil on Windows,
// without a home, or when this pterm cannot be located: no GitHub feature.
// The broker sets DataDir and Profile.
func githubConfig() *github.Config {
	home, err := os.UserHomeDir()
	if runtime.GOOS == "windows" || err != nil || !filepath.IsAbs(home) {
		return nil
	}
	// Not resolved through symlinks: each clone's helper line keeps this path,
	// and the path that started the broker (~/.local/bin/pterm) outlives an
	// update; a resolved versioned path might not.
	pterm, err := os.Executable()
	if err != nil || !filepath.IsAbs(pterm) {
		return nil
	}
	paths := []string{"/usr/bin/git", "/usr/local/bin/git"}
	if runtime.GOOS == "darwin" {
		paths = []string{"/opt/homebrew/bin/git", "/usr/local/bin/git", "/usr/bin/git"}
	}
	return &github.Config{Home: home, GitPaths: paths, PathEnv: os.Getenv("PATH"), Pterm: pterm}
}

// githubStatusLine is the one line of `pterm github status`.
func githubStatusLine(status protocol.AdminRPCGitHubStatusPayload) string {
	if status.State == nil {
		return "GitHub: not available in this build"
	}
	switch *status.State {
	case protocol.HTTPGitHubStateReady, protocol.NeedsInstall:
		if status.Login == nil {
			return "GitHub: connected"
		}
		if *status.State == protocol.NeedsInstall {
			return "GitHub: connected as " + *status.Login + "; choose repositories on GitHub"
		}
		return "GitHub: connected as " + *status.Login
	case protocol.Connecting:
		return "GitHub: connecting"
	case protocol.NoGit:
		return "GitHub: git is not installed on this computer"
	default:
		return "GitHub: not connected"
	}
}

func writeGitHubStatus(status protocol.AdminRPCGitHubStatusPayload, output io.Writer) error {
	_, err := fmt.Fprintln(output, githubStatusLine(status))
	return err
}

func writeGitHubDisconnect(status protocol.AdminRPCGitHubStatusPayload, output io.Writer) error {
	if status.State == nil {
		return writeGitHubStatus(status, output)
	}
	_, err := fmt.Fprintf(output, "GitHub: disconnected\n%s\n", githubRevokeHint)
	return err
}

// inspectGitHub prints the GitHub line of `pterm doctor` when the broker
// answers with a state. It never counts a problem.
func inspectGitHub(ctx context.Context, env lifecycleEnv, report hostReport, output io.Writer) {
	if !report.broker || report.listErr != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, ipc.HandshakeTimeout+2*time.Second)
	defer cancel()
	admin, err := localclient.DialAdmin(ctx, env.paths.RuntimeDir, buildID)
	if err != nil {
		return
	}
	defer admin.Close()
	status, err := admin.GitHubStatus(ctx)
	if err != nil || status.State == nil {
		return
	}
	if status.Login != nil && (*status.State == protocol.HTTPGitHubStateReady || *status.State == protocol.NeedsInstall) {
		fmt.Fprintf(output, "  GitHub: connected as %s\n", *status.Login)
		return
	}
	fmt.Fprintln(output, "  GitHub: not connected")
}

// readGitCredentialInput reads git's key=value lines up to a blank line or
// EOF, within the caps. Unknown keys are kept and ignored by the caller.
func readGitCredentialInput(input io.Reader) (map[string]string, error) {
	reader := bufio.NewReader(io.LimitReader(input, gitCredentialMaxBytes+1))
	values := map[string]string{}
	total, lines := 0, 0
	for {
		line, err := reader.ReadString('\n')
		total += len(line)
		if total > gitCredentialMaxBytes {
			return nil, errors.New("credential input is too large")
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		trimmed := strings.TrimSuffix(line, "\n")
		if trimmed == "" {
			return values, nil // a blank line, or EOF
		}
		if lines++; lines > gitCredentialMaxLines {
			return nil, errors.New("credential input has too many lines")
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok || key == "" || strings.IndexByte(trimmed, 0) >= 0 {
			return nil, errors.New("credential input is malformed")
		}
		values[key] = value
		if errors.Is(err, io.EOF) {
			return values, nil
		}
	}
}

// runGitHubCredential is git's credential helper: only `get` for https on
// github.com answers, with the broker's access token. It returns the exit
// code. The token goes to stdout only, never into an error or a log.
func runGitHubCredential(
	ctx context.Context, args []string, input io.Reader, stdout, stderr io.Writer,
	credential func(context.Context) (protocol.AdminRPCGitHubCredentialPayload, error),
) int {
	values, err := readGitCredentialInput(input)
	if err != nil {
		return 1
	}
	e2e := githubE2EHost()
	github := values["protocol"] == "https" && values["host"] == "github.com" ||
		e2e != "" && values["host"] == e2e && (values["protocol"] == "http" || values["protocol"] == "https")
	if len(args) != 1 || args[0] != "get" || !github {
		return 0
	}
	answer, err := credential(ctx)
	if err != nil {
		// quit=1: git asks no other helper and does not prompt for a password.
		fmt.Fprintln(stderr, githubNotConnected)
		fmt.Fprintln(stdout, "quit=1")
		return 1
	}
	text := "username=" + answer.Username + "\npassword=" + answer.Password + "\n"
	if answer.ExpiresAt > 0 {
		text += fmt.Sprintf("password_expiry_utc=%d\n", answer.ExpiresAt)
	}
	if _, err := io.WriteString(stdout, text); err != nil {
		return 1
	}
	return 0
}

// githubCredentialFlags takes the flags a development broker writes before
// git's operation: --dev and --runtime-dir <dir>. Anything else is left for
// runGitHubCredential, which answers only "get".
func githubCredentialFlags(args []string) (rest []string, dev bool, runtimeDir string) {
	for len(args) > 0 {
		switch {
		case args[0] == "--dev":
			args, dev = args[1:], true
		case args[0] == "--runtime-dir" && len(args) > 1:
			args, runtimeDir = args[2:], args[1]
		default:
			return args, dev, runtimeDir
		}
	}
	return args, dev, runtimeDir
}

// brokerGitHubCredential asks the broker that the helper line names (--dev,
// --runtime-dir), else the production broker, after the same runtime folder
// checks as the other admin commands.
func brokerGitHubCredential(ctx context.Context, dev bool, flagRuntimeDir string) (protocol.AdminRPCGitHubCredentialPayload, error) {
	runtimeDir, err := adminRuntimeDir(dev, flagRuntimeDir)
	if err != nil {
		return protocol.AdminRPCGitHubCredentialPayload{}, err
	}
	// The broker may refresh the token at GitHub (10 s per request).
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := localclient.DialAdmin(ctx, runtimeDir, buildID)
	if err != nil {
		return protocol.AdminRPCGitHubCredentialPayload{}, err
	}
	defer client.Close()
	return client.GitHubCredential(ctx)
}
