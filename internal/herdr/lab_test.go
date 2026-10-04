//go:build !windows

package herdr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
)

// lab is an isolated real herdr: a copy of the installed binary, a temp
// HOME and its own named session (docs/herdr-provider.md, section 15). It
// never reads the owner's HOME and stops only its own session.
type lab struct {
	t       *testing.T
	root    string
	binary  string
	session string
	env     []string
}

// TestLab runs only with PTERM_HERDR_LAB=1 (and PTERM_HERDR_LAB_BINARY to
// pick a herdr other than the one on PATH).
func TestLab(t *testing.T) {
	if os.Getenv("PTERM_HERDR_LAB") != "1" {
		t.Skip("opt-in: PTERM_HERDR_LAB=1 go test ./internal/herdr -run Lab")
	}
	lab := startLab(t)
	ctx := context.Background()
	repo := lab.repo("shop")
	notes := filepath.Join(lab.root, "notes")
	if err := os.MkdirAll(notes, 0o700); err != nil {
		t.Fatal(err)
	}
	lab.run("workspace", "create", "--cwd", repo, "--label", "shop", "--no-focus")
	lab.run("workspace", "create", "--cwd", notes, "--label", "notes", "--no-focus")
	lab.run("worktree", "create", "--workspace", "w1", "--branch", "silver-meadow", "--no-focus")
	lab.run("pane", "report-agent", "w1:p1", "--source", "lab", "--agent", "claude", "--state", "blocked")
	lab.run("pane", "report-agent", "w3:p1", "--source", "lab", "--agent", "codex", "--state", "working")

	var mu sync.Mutex
	var changes []Change
	provider := New(Config{BinaryPaths: []string{lab.binary}, Home: lab.root, Session: lab.session}, func(change Change) {
		mu.Lock()
		changes = append(changes, change)
		mu.Unlock()
	})
	provider.step(ctx)
	info, installed := provider.Info()
	if !installed || info.State != protocol.HTTPProviderStateReady || info.Version == nil || len(provider.caps.kinds) == 0 {
		t.Fatalf("info = %+v, kinds %v", info, provider.caps.kinds)
	}
	snapshot := provider.Snapshot()
	if err := protocol.ValidateProviderSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 2 || snapshot.Groups[0].Name != "shop" || len(snapshot.Groups[0].Items) != 2 ||
		snapshot.Groups[0].State != "blocked" || snapshot.Groups[0].Items[1].State != "working" || snapshot.Groups[1].Name != "notes" {
		t.Fatalf("mapping: %+v", snapshot)
	}
	// idle reported to a headless server is done (unseen).
	lab.run("pane", "report-agent", "w3:p1", "--source", "lab", "--agent", "codex", "--state", "idle")
	provider.step(ctx)
	if state := provider.Snapshot().Groups[0].Items[1].State; state != "done" {
		t.Fatalf("idle on a headless server = %s", state)
	}

	target := *provider.Snapshot().Groups[0].Items[0].OpenTarget
	first := lab.attach(provider, target)
	time.Sleep(time.Second)
	// The attach gives the pane its own size.
	first.write("stty size\r")
	lab.waitPane("w1:p1", "20 45")
	// A second direct attach is refused (never --takeover).
	second := lab.attach(provider, target)
	if err := second.wait(); err == nil || !strings.Contains(second.output(), "already has an attached client") {
		t.Fatalf("second attach: %v %q", err, second.output())
	}
	// ctrl+b ctrl+b (what the broker sends for one ctrl+b) reaches the
	// program as one literal ctrl+b.
	first.write("cat -v\r")
	time.Sleep(300 * time.Millisecond)
	first.write("\x02\x02\r")
	lab.waitPane("w1:p1", "^B")
	first.write("\x03")
	// ctrl+b q detaches with exit 0.
	first.write("\x02q")
	if err := first.wait(); err != nil {
		t.Fatalf("detach: %v", err)
	}

	// M2 create, without an agent: the lab starts no real agent program.
	group := provider.Snapshot().Groups[0].ID
	created, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: group, Name: "lab-tree"})
	if err != nil || created.AgentStarted || created.Problem != nil {
		t.Fatalf("create = %+v %v", created, err)
	}
	provider.step(ctx)
	if _, err := provider.Attach(created.TargetID); err != nil && !errors.Is(err, ErrTargetMissing) {
		t.Fatal(err)
	}
	found := false
	for _, item := range provider.Snapshot().Groups[0].Items {
		found = found || item.ID == created.ItemID && item.Name == "lab-tree"
	}
	if !found {
		t.Fatalf("new worktree not in the snapshot: %+v", provider.Snapshot().Groups[0])
	}
	if _, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: group, Name: "lab-tree"}); !errors.Is(err, ErrExists) {
		t.Fatalf("second create = %v", err)
	}
	notesGroup := provider.Snapshot().Groups[1].ID
	if _, err := provider.Create(ctx, protocol.HTTPProviderCreateRequest{GroupID: notesGroup, Name: "x"}); !errors.Is(err, ErrNotGit) {
		t.Fatalf("create outside git = %v", err)
	}
}

func startLab(t *testing.T) *lab {
	t.Helper()
	source := os.Getenv("PTERM_HERDR_LAB_BINARY")
	if source == "" {
		found, err := exec.LookPath("herdr")
		if err != nil {
			t.Skip("no herdr on PATH")
		}
		source = found
	}
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "termivoa-lab-")
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := os.UserHomeDir(); strings.HasPrefix(root, real) {
		t.Fatal("the lab HOME must not be inside the real home")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "bin", "herdr")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".zshrc"), []byte("PROMPT='%# '\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No background calls to herdr.dev from the lab.
	if err := os.MkdirAll(filepath.Join(root, ".config", "herdr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".config", "herdr", "config.toml"),
		[]byte("[update]\nversion_check = false\nmanifest_check = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	lab := &lab{
		t: t, root: root, binary: binary, session: "termivoa-lab-" + hex.EncodeToString(suffix),
		env: []string{"HOME=" + root, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "SHELL=/bin/zsh", "TERM=xterm-256color", "LANG=en_US.UTF-8"},
	}
	server := exec.Command(binary, "--session", lab.session, "server")
	server.Env, server.Dir = lab.env, root
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, binary, "session", "stop", lab.session)
		stop.Env, stop.Dir = lab.env, root
		_ = stop.Run()
		done := make(chan struct{})
		go func() { _ = server.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = server.Process.Kill()
		}
		_ = os.RemoveAll(root)
	})
	socket := filepath.Join(root, ".config", "herdr", "sessions", lab.session, "herdr.sock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			return lab
		}
		if time.Now().After(deadline) {
			t.Fatal("lab server did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (lab *lab) run(args ...string) string {
	lab.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, lab.binary, append([]string{"--session", lab.session}, args...)...)
	command.Env, command.Dir = lab.env, lab.root
	output, err := command.CombinedOutput()
	if err != nil {
		lab.t.Fatalf("herdr %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func (lab *lab) repo(name string) string {
	lab.t.Helper()
	repo := filepath.Join(lab.root, name)
	for _, args := range [][]string{{"init", "-q", repo}, {"-C", repo, "-c", "user.email=lab@example.com", "-c", "user.name=lab", "commit", "-q", "--allow-empty", "-m", "init"}} {
		command := exec.Command("git", args...)
		command.Env = lab.env
		if output, err := command.CombinedOutput(); err != nil {
			lab.t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repo
}

func (lab *lab) waitPane(pane, want string) {
	lab.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(lab.run("pane", "read", pane), want) {
		if time.Now().After(deadline) {
			lab.t.Fatalf("pane %s never showed %q:\n%s", pane, want, lab.run("pane", "read", pane))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type labClient struct {
	t       *testing.T
	process *ptyhost.Process
	mu      sync.Mutex
	text    strings.Builder
}

// attach starts the provider's argv in a 45x20 PTY, as a provider session
// does, and keeps draining it.
func (lab *lab) attach(provider *Provider, target string) *labClient {
	lab.t.Helper()
	found, err := provider.Attach(target)
	if err != nil {
		lab.t.Fatal(err)
	}
	process, err := ptyhost.Start(context.Background(), ptyhost.Spec{
		Executable: found.Argv[0], Args: found.Argv[1:], Env: found.Env, Directory: lab.root, Columns: 45, Rows: 20,
	})
	if err != nil {
		lab.t.Fatal(err)
	}
	lab.t.Cleanup(func() { _ = process.Close() })
	client := &labClient{t: lab.t, process: process}
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := process.Read(buffer)
			client.mu.Lock()
			client.text.Write(buffer[:count])
			client.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return client
}

func (client *labClient) write(text string) {
	client.t.Helper()
	if _, err := client.process.Write([]byte(text)); err != nil {
		client.t.Fatal(err)
	}
}

func (client *labClient) output() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.text.String()
}

func (client *labClient) wait() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.process.Wait(ctx)
}
