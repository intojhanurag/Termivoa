package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
)

func TestDoctorHerdrLine(t *testing.T) {
	var output bytes.Buffer
	inspectHerdr(context.Background(), nil, &output)
	root, err := os.MkdirTemp("/tmp", "herdr-doctor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	home := filepath.Join(root, "home")
	missing := herdr.Config{BinaryPaths: []string{filepath.Join(root, "bin", "herdr")}, Home: home}
	inspectHerdr(context.Background(), &missing, &output)
	if output.Len() != 0 {
		t.Fatalf("a computer without herdr prints %q", output.String())
	}

	binary, err := herdrtest.Script(filepath.Join(root, "bin"), true, "claude, codex")
	if err != nil {
		t.Fatal(err)
	}
	inspectHerdr(context.Background(), &missing, &output)
	if want := "  herdr: " + binary + ", server not running ("; !strings.HasPrefix(output.String(), want) {
		t.Fatalf("not running:\n%s", output.String())
	}
	server, err := herdrtest.Listen(filepath.Join(home, ".config", "herdr", "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	server.Version("0.9.3", 22)
	server.Snapshot([]byte(`{"workspaces":[],"tabs":[],"panes":[],"layouts":[],"agents":[]}`))
	if err := os.WriteFile(filepath.Join(home, ".config", "herdr", "config.toml"), []byte("[keys]\nprefix = \"ctrl+a\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	inspectHerdr(context.Background(), &missing, &output)
	if !strings.Contains(output.String(), "  herdr: "+binary+" 0.9.3 (protocol 22), server running at ") ||
		!strings.Contains(output.String(), "can make worktrees") || !strings.Contains(output.String(), "  ! herdr: config.toml sets a prefix key") {
		t.Fatalf("running:\n%s", output.String())
	}
	// A folder others can write (Ubuntu's umask 002): the line says why the
	// socket is not used and the exact command, not just "not running".
	configFolder := filepath.Join(home, ".config")
	if err := os.Chmod(configFolder, 0o775); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	inspectHerdr(context.Background(), &missing, &output)
	if want := "  ! herdr: " + binary + ", Termivoa will not use the server socket " + server.Path + ": group or others can write " +
		configFolder + "; run: chmod go-w " + configFolder + "\n"; !strings.Contains(output.String(), want) {
		t.Fatalf("refused socket:\n%s\nwant %q", output.String(), want)
	}
}
