//go:build darwin && nativecompat

package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const shellFixturePrompt = "PTERM> "

// Explicit native-app suite: go test -tags=nativecompat ./tests/integration
// -run '^TestNativeZsh' -v. Nothing here reads the owner's shell configuration,
// uses an account, or writes outside a disposable project/home directory.
func TestNativeZshStructuredEditingInterruptEOFAndPaste(t *testing.T) {
	versionContext, cancelVersion := context.WithTimeout(context.Background(), 5*time.Second)
	version, err := exec.CommandContext(versionContext, "/bin/zsh", "-f", "--version").Output()
	cancelVersion()
	if err != nil {
		t.Fatalf("required native zsh unavailable: %v", err)
	}
	t.Logf("native shell: %s", strings.TrimSpace(string(version)))
	fixture := startNativeZsh(t)
	fixture.wait(t, "initial prompt", shellPromptReady)
	initial, err := fixture.session.Status(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}

	// ArrowLeft and Backspace replace x without changing any launcher args.
	fixture.send(t, shellText("print -r -- PTERM_EDIT_axc"), shellKey(protocol.ArrowLeft),
		shellKey(protocol.Backspace), shellText("b"), shellKey(protocol.Enter))
	fixture.wait(t, "edited command output", func(frame terminal.Frame) bool {
		return shellHasLine(frame, "PTERM_EDIT_abc") && shellPromptReady(frame)
	})
	// Ctrl+A/Ctrl+E edit the beginning and end of an actual ZLE command line.
	fixture.send(t, shellText("rint -r -- PTERM_NAV"), shellControl("a"), shellText("p"),
		shellControl("e"), shellText("_OK"), shellKey(protocol.Enter))
	fixture.wait(t, "beginning/end editing", func(frame terminal.Frame) bool {
		return shellHasLine(frame, "PTERM_NAV_OK") && shellPromptReady(frame)
	})

	fixture.send(t, shellText("print -r -- PTERM_MUST_NOT_RUN"))
	fixture.wait(t, "draft before Ctrl+C", func(frame terminal.Frame) bool {
		return strings.Contains(shellCursorLine(frame), "PTERM_MUST_NOT_RUN")
	})
	fixture.send(t, shellControl("c"))
	frame := fixture.wait(t, "prompt after draft cancellation", shellPromptReady)
	if shellHasLine(frame, "PTERM_MUST_NOT_RUN") {
		t.Fatal("Ctrl+C executed the draft")
	}
	fixture.send(t, shellText("print -r -- PTERM_SLEEP_STARTED; /bin/sleep 20"), shellKey(protocol.Enter))
	fixture.wait(t, "foreground command start", func(frame terminal.Frame) bool {
		return shellHasLine(frame, "PTERM_SLEEP_STARTED") && !shellPromptReady(frame)
	})
	fixture.send(t, shellControl("c"))
	fixture.wait(t, "prompt after foreground interrupt", shellPromptReady)
	fixture.send(t, shellText("print -r -- PTERM_INTERRUPT_OK"), shellKey(protocol.Enter))
	fixture.wait(t, "shell still accepts input", func(frame terminal.Frame) bool {
		return shellHasLine(frame, "PTERM_INTERRUPT_OK") && shellPromptReady(frame)
	})
	current, err := fixture.session.Status(fixture.ctx)
	if err != nil || current.State != broker.SessionRunning || current.PID != initial.PID || current.Generation != initial.Generation {
		t.Fatalf("Ctrl+C changed managed shell identity: %+v, %v", current, err)
	}

	// Zsh enables bracketed paste at its prompt. A multiline paste must stay
	// in ZLE until a separate structured Enter; a synthetic file proves that
	// the first line did not execute merely because its newline was pasted.
	proof := filepath.Join(fixture.directory, "paste-proof")
	fixture.send(t, broker.InputAction{Kind: broker.InputKindPaste,
		Text: "print -r -- PTERM_PASTE_ONE > paste-proof\nprint -r -- PTERM_PASTE_TWO"})
	fixture.wait(t, "multiline paste in editing buffer", func(frame terminal.Frame) bool {
		return strings.Contains(shellCursorLine(frame), "print -r -- PTERM_PASTE_TWO")
	})
	if _, err := os.Stat(proof); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("multiline paste executed before separate Enter: %v", err)
	}
	fixture.send(t, shellKey(protocol.Enter))
	fixture.wait(t, "explicitly submitted multiline paste", func(frame terminal.Frame) bool {
		return shellHasLine(frame, "PTERM_PASTE_TWO") && shellPromptReady(frame)
	})
	if contents, err := os.ReadFile(proof); err != nil || string(contents) != "PTERM_PASTE_ONE\n" {
		t.Fatalf("submitted paste proof = %q, %v", contents, err)
	}

	fixture.send(t, shellText("print -r -- 'PTERM_UNICODE_界_é_👩‍💻'"), shellKey(protocol.Enter))
	fixture.wait(t, "Unicode shell output", func(frame terminal.Frame) bool {
		return shellHasLine(frame, "PTERM_UNICODE_界_é_👩‍💻") && shellPromptReady(frame)
	})
	fixture.send(t, shellControl("d"))
	select {
	case <-fixture.session.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+D at an empty prompt did not exit zsh")
	}
	final, ok := fixture.session.FinalStatus()
	if !ok || final.State != broker.SessionExited || final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("Ctrl+D lifecycle = %+v, available=%t", final, ok)
	}
}

func TestNativeZshPagerAndEditorAlternateScreens(t *testing.T) {
	for _, application := range []string{"/usr/bin/less", "/usr/bin/vim"} {
		versionContext, cancelVersion := context.WithTimeout(context.Background(), 5*time.Second)
		version, err := exec.CommandContext(versionContext, application, "--version").Output()
		cancelVersion()
		if err != nil {
			t.Fatalf("required native application unavailable: %s: %v", application, err)
		}
		t.Logf("native application: %s", strings.SplitN(string(version), "\n", 2)[0])
	}
	fixture := startNativeZsh(t)
	fixture.wait(t, "initial shell prompt", shellPromptReady)
	var pages strings.Builder
	for number := 1; number <= 80; number++ {
		fmt.Fprintf(&pages, "PTERM_PAGE_%03d\n", number)
	}
	if err := os.WriteFile(filepath.Join(fixture.directory, "pager.txt"), []byte(pages.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.send(t, shellText("/usr/bin/less -- pager.txt"), shellKey(protocol.Enter))
	fixture.wait(t, "less alternate screen", func(frame terminal.Frame) bool {
		return frame.Alternate && shellHasLine(frame, "PTERM_PAGE_001")
	})
	fixture.send(t, shellKey(protocol.PageDown))
	fixture.wait(t, "less PageDown", func(frame terminal.Frame) bool {
		return frame.Alternate && strings.HasPrefix(shellRow(frame.Screen[0]), "PTERM_PAGE_") && !shellHasLine(frame, "PTERM_PAGE_001")
	})
	fixture.send(t, shellText("G"))
	fixture.wait(t, "less final page", func(frame terminal.Frame) bool {
		return frame.Alternate && shellHasLine(frame, "PTERM_PAGE_080")
	})
	fixture.send(t, shellText("q"))
	fixture.wait(t, "shell restored after less", func(frame terminal.Frame) bool {
		return !frame.Alternate && shellPromptReady(frame)
	})

	edited := filepath.Join(fixture.directory, "editor.txt")
	if err := os.WriteFile(edited, []byte("PTERM_VIM_ORIGINAL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No vimrc, gvimrc, viminfo, plugins, or swap files. Only the disposable
	// synthetic document is edited; no clipboard or external provider is used.
	fixture.send(t, shellText("/usr/bin/vim -N -u NONE -U NONE -i NONE --noplugin -n -- editor.txt"), shellKey(protocol.Enter))
	fixture.wait(t, "vim alternate screen", func(frame terminal.Frame) bool {
		return frame.Alternate && shellHasLine(frame, "PTERM_VIM_ORIGINAL")
	})
	fixture.send(t, shellText("iPTERM_VIM_EDITED "), shellKey(protocol.Escape), shellText(":wq"), shellKey(protocol.Enter))
	fixture.wait(t, "shell restored after vim", func(frame terminal.Frame) bool {
		return !frame.Alternate && shellPromptReady(frame)
	})
	if contents, err := os.ReadFile(edited); err != nil || string(contents) != "PTERM_VIM_EDITED PTERM_VIM_ORIGINAL\n" {
		t.Fatalf("native editor result = %q, %v", contents, err)
	}
	fixture.send(t, shellControl("d"))
	select {
	case <-fixture.session.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("shell did not exit after native application checks")
	}
}

type nativeShellFixture struct {
	ctx       context.Context
	session   *broker.Session
	identity  broker.MutationIdentity
	sequence  uint64
	directory string
}

func startNativeZsh(t *testing.T) *nativeShellFixture {
	t.Helper()
	directory := t.TempDir()
	// If -f regresses, only this synthetic startup file can execute. The
	// owner's real home, dotfiles, history, and exported credentials are absent.
	if err := os.WriteFile(filepath.Join(directory, ".zshrc"), []byte("exit 93\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	const generation = "e2222222-2222-4222-8222-222222222222"
	session, err := broker.StartSession(ctx, broker.SessionConfig{
		ID: "e1111111-1111-4111-8111-111111111111", Generation: generation,
		Process: ptyhost.Spec{Executable: "/usr/bin/env", Args: []string{
			"-i", "HOME=" + directory, "ZDOTDIR=" + directory, "PATH=/usr/bin:/bin",
			"TERM=xterm-256color", "COLORTERM=truecolor", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8",
			"PS1=" + shellFixturePrompt, "PS2=CONT> ", "RPS1=", "HISTFILE=/dev/null",
			"/bin/zsh", "-f", "-i",
		}, Directory: directory, Columns: 80, Rows: 24},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("native shell cleanup: %v", err)
		}
	})
	renderer, err := desktop.NewRenderer(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := desktop.NewFrameClient(renderer)
	if err != nil {
		t.Fatal(err)
	}
	sink := &nativeShellSink{frames: frames, snapshots: make(chan protocol.SnapshotEndPayload, 1)}
	viewer, err := session.AttachViewer(ctx, broker.ViewerConfig{
		ConnectionID: "e3333333-3333-4333-8333-333333333333", DeviceID: "e4444444-4444-4444-8444-444444444444",
		Generation: generation, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := viewer.Close(); err != nil {
			t.Errorf("native shell frame consumer: %v", err)
		}
	})
	select {
	case snapshot := <-sink.snapshots:
		revision, err := strconv.ParseUint(snapshot.Revision, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if err := viewer.SnapshotApplied(ctx, snapshot.SnapshotID, revision); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("native shell observer did not receive a complete snapshot")
	}
	grant, err := session.AcquireControl(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	return &nativeShellFixture{ctx: ctx, session: session, directory: directory, identity: broker.MutationIdentity{
		Generation: generation, ConnectionID: grant.ConnectionID, DeviceID: grant.DeviceID, Lease: grant.Lease,
	}}
}

func (fixture *nativeShellFixture) send(t *testing.T, actions ...broker.InputAction) {
	t.Helper()
	if _, err := fixture.session.Heartbeat(fixture.ctx, fixture.identity); err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		fixture.sequence++
		if _, err := fixture.session.SubmitInput(fixture.ctx, fixture.identity, fixture.sequence, action); err != nil {
			t.Fatalf("structured input %d was rejected: %v", fixture.sequence, err)
		}
	}
}

func (fixture *nativeShellFixture) wait(t *testing.T, phase string, ready func(terminal.Frame) bool) terminal.Frame {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		frame, err := fixture.session.Snapshot(fixture.ctx)
		if err != nil {
			t.Fatalf("%s snapshot: %v", phase, err)
		}
		if ready(frame) {
			return frame
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("%s not observed in canonical grid; synthetic cursor row=%q", phase, shellCursorLine(frame))
		case <-fixture.ctx.Done():
			t.Fatalf("%s: %v", phase, fixture.ctx.Err())
		}
	}
}

func shellText(text string) broker.InputAction {
	return broker.InputAction{Kind: broker.InputKindText, Text: text}
}
func shellKey(key protocol.KeyName) broker.InputAction {
	return broker.InputAction{Kind: broker.InputKindKey, Key: key}
}
func shellControl(character string) broker.InputAction {
	return broker.InputAction{Kind: broker.InputKindKey, Key: protocol.Character, Character: &character, Modifiers: protocol.Modifiers{Ctrl: true}}
}
func shellRow(row terminal.Row) string {
	var text strings.Builder
	for _, cell := range row.Cells {
		if !cell.Continuation {
			text.WriteString(cell.Text)
		}
	}
	return strings.TrimRight(text.String(), " ")
}
func shellCursorLine(frame terminal.Frame) string { return shellRow(frame.Screen[frame.Cursor.Row]) }
func shellPromptReady(frame terminal.Frame) bool {
	return shellCursorLine(frame) == strings.TrimSpace(shellFixturePrompt)
}
func shellHasLine(frame terminal.Frame, wanted string) bool {
	for _, row := range frame.Screen {
		if shellRow(row) == wanted {
			return true
		}
	}
	return false
}

// A bounded headless observer validates normalized frames with the actual
// desktop frame adapter. Drawing is discarded; assertions use canonical state.
// This is not evidence about a physical terminal's rendering or glyph width.
type nativeShellSink struct {
	frames    *desktop.FrameClient
	snapshots chan protocol.SnapshotEndPayload
}

func (sink *nativeShellSink) Send(ctx context.Context, data []byte, drained func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	message, err := protocol.DecodeHost(data)
	if err != nil {
		return err
	}
	switch message.Type {
	case protocol.SnapshotBegin:
		payload, _ := protocol.DecodePayload[protocol.SnapshotBeginPayload](message)
		err = sink.frames.BeginSnapshot(*message.Generation, payload)
	case protocol.SnapshotRows:
		payload, _ := protocol.DecodePayload[protocol.SnapshotRowsPayload](message)
		err = sink.frames.AddSnapshotRows(*message.Generation, payload)
	case protocol.SnapshotEnd:
		payload, _ := protocol.DecodePayload[protocol.SnapshotEndPayload](message)
		err = sink.frames.EndSnapshot(*message.Generation, payload)
		if err == nil {
			select {
			case sink.snapshots <- payload:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	case protocol.ScreenPatch:
		payload, _ := protocol.DecodePayload[protocol.ScreenPatchPayload](message)
		err = sink.frames.ApplyPatch(*message.Generation, payload)
	}
	if err != nil {
		return err
	}
	drained()
	return nil
}
