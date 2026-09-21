//go:build darwin || windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/cli"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const (
	commandProjectID = "11111111-1111-4111-8111-111111111111"
	commandProfileID = "22222222-2222-4222-8222-222222222222"
	commandHelper    = "pterm-command-fixture"
)

func TestCommandManagedShellHelper(t *testing.T) {
	if os.Args[len(os.Args)-1] != commandHelper {
		return
	}
	// This test executable is a disposable PTY child, not a user's shell or
	// project. It remains alive until explicit managed-session termination.
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		_, _ = io.WriteString(os.Stdout, "fixture alive\r\n")
	}
}

func TestManagementCommandsAgainstNativeBroker(t *testing.T) {
	runtimeDir, client := startCommandBroker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runCommand := func(command string, input *os.File, extra ...string) (string, error) {
		t.Helper()
		var output bytes.Buffer
		args := append([]string{command, "--dev", "--runtime-dir", runtimeDir}, extra...)
		err := runWithInput(ctx, args, input, &output)
		return output.String(), err
	}
	if output, err := runCommand("list", nil); err != nil || output != "No managed sessions.\n" {
		t.Fatalf("empty list = %q, %v", output, err)
	}
	operationID, err := cli.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.Create(ctx, operationID, commandProjectID, commandProfileID)
	if err != nil {
		t.Fatal(err)
	}
	ref := localclient.SessionRef{ID: created.Session.ID, Generation: created.Session.Generation}
	if output, err := runCommand("list", nil); err != nil || !strings.Contains(output, ref.ID) || !strings.Contains(output, "running") {
		t.Fatalf("live list = %q, %v", output, err)
	}
	declined := confirmationPipe(t, "no\n")
	if output, err := runCommand("terminate", declined, ref.ID); err != nil || !strings.Contains(output, "Termination cancelled.") {
		t.Fatalf("declined termination = %q, %v", output, err)
	}
	if status, err := client.Status(ctx, ref); err != nil || status.State != protocol.Running {
		t.Fatalf("declined termination changed live state: %#v, %v", status, err)
	}
	approved := confirmationPipe(t, "yes\n")
	if output, err := runCommand("terminate", approved, ref.ID); err != nil || !strings.Contains(output, "Termination") {
		t.Fatalf("approved termination = %q, %v", output, err)
	}
	for {
		status, err := client.Status(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == protocol.Exited {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("terminated session did not exit")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Nil stdin proves an already-exited session needs no further confirmation.
	if output, err := runCommand("terminate", nil, ref.ID); err != nil || !strings.Contains(output, "already exited") {
		t.Fatalf("repeat termination = %q, %v", output, err)
	}
	if output, err := runCommand("list", nil); err != nil || !strings.Contains(output, "exited") {
		t.Fatalf("final list = %q, %v", output, err)
	}
}

func TestFileConfirmerCancellationLeavesInputOpenAndNoLateRead(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		confirmed, err := (fileConfirmer{input: input, output: promptWriter{ready: ready}}).Confirm(ctx, "Confirm?")
		if confirmed {
			result <- errors.New("canceled confirmation succeeded")
			return
		}
		result <- err
	}()
	<-ready
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("confirmation cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmation did not cancel")
	}
	if _, err := writer.Write([]byte("yes\n")); err != nil {
		t.Fatal(err)
	}
	var remaining [4]byte
	if _, err := io.ReadFull(input, remaining[:]); err != nil || string(remaining[:]) != "yes\n" {
		t.Fatalf("caller input closed or consumed after cancellation: %q, %v", remaining, err)
	}
}

type promptWriter struct{ ready chan<- struct{} }

func (writer promptWriter) Write(data []byte) (int, error) {
	close(writer.ready)
	return len(data), nil
}

func confirmationPipe(t *testing.T, value string) *os.File {
	t.Helper()
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	if _, err := io.WriteString(output, value); err != nil {
		_ = output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return input
}

func startCommandBroker(t *testing.T) (string, *localclient.ManagementClient) {
	t.Helper()
	tempParent := ""
	if runtime.GOOS == "darwin" {
		tempParent = "/tmp" // Keep the Unix socket path within the OS byte limit.
	}
	root, err := os.MkdirTemp(tempParent, "pterm-command-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dataDir, runtimeDir := filepath.Join(root, "data"), filepath.Join(root, "runtime")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Seed synthetic saved configuration before the broker becomes the sole
	// database owner. No CLI command reads or writes SQLite directly.
	persistence, err := store.Open(context.Background(), filepath.Join(dataDir, "phone-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = persistence.PutProject(context.Background(), store.Project{
		ID: commandProjectID, DisplayName: "CLI fixture", CanonicalPath: root, CreatedAt: now, UpdatedAt: now,
	})
	if err == nil {
		err = persistence.PutShellProfile(context.Background(), store.ShellProfile{
			ID: commandProfileID, DisplayName: "fixture", Platform: store.Platform(runtime.GOOS),
			ExecutablePath: fixture, Arguments: []string{"-test.run=^TestCommandManagedShellHelper$", "--", commandHelper},
			CreatedAt: now, UpdatedAt: now,
		})
	}
	if err := errors.Join(err, persistence.Close()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("broker shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("fixture broker failed to shut down")
		}
	})
	go func() {
		done <- host.RunBroker(ctx, host.BrokerConfig{
			DataDir: dataDir, RuntimeDir: runtimeDir, HostBuild: "command-test", Profile: host.DevelopmentProfile,
			Ready: func(host.BrokerReady) { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		done <- err // Preserve the result for cleanup.
		t.Fatalf("broker failed before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("broker was not ready")
	}
	client, err := localclient.DialManagement(ctx, runtimeDir, "command-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return runtimeDir, client
}
