package projecttasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// commands lists "source name kind: command" per task.
func commands(payload protocol.TasksListedPayload) []string {
	var lines []string
	for _, task := range payload.Tasks {
		lines = append(lines, fmt.Sprintf("%s %s %s: %s", task.Source, task.Name, task.Kind, task.Command))
	}
	return lines
}

func list(t *testing.T, dir string) protocol.TasksListedPayload {
	t.Helper()
	payload := List(context.Background(), dir)
	if payload.Folder == nil || *payload.Folder != dir || payload.Tasks == nil {
		t.Fatalf("payload = %#v", payload)
	}
	return payload
}

func TestPackageManagerFromLockfile(t *testing.T) {
	scripts := `{"scripts":{"build":"tsc","check":"tsc --noEmit"},"packageManager":"yarn@4.0.0"}`
	for lock, want := range map[string][]string{
		"":                  {"yarn build", "yarn run check"}, // packageManager field, no lockfile
		"pnpm-lock.yaml":    {"pnpm run build", "pnpm run check"},
		"yarn.lock":         {"yarn build", "yarn run check"},
		"bun.lock":          {"bun run build", "bun run check"},
		"bun.lockb":         {"bun run build", "bun run check"},
		"package-lock.json": {"npm run build", "npm run check"},
	} {
		files := map[string]string{"package.json": scripts}
		if lock != "" {
			files[lock] = ""
		}
		var got []string
		for _, task := range list(t, project(t, files)).Tasks {
			got = append(got, task.Command)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", lock, got, want)
		}
	}
	payload := list(t, project(t, map[string]string{"package.json": `{"scripts":{"dev":"vite"}}`}))
	if payload.State != protocol.TasksStateReady || payload.Tasks[0].Command != "npm run dev" {
		t.Errorf("default = %#v", payload)
	}
}

func TestUnsafeScriptNamesAreSkipped(t *testing.T) {
	payload := list(t, project(t, map[string]string{"package.json": `{"scripts":{
		"a b":"x", "a;rm -rf ~":"x", "$(id)":"x", "-f":"x", "é":"x", "":"x", "number":1,
		"` + strings.Repeat("a", 129) + `":"x", "test:unit/@a+b.c_d-1":"x", "test:unit/@a+b.c_d-1":"again"}}`}))
	if got := commands(payload); !slices.Equal(got, []string{"package.json test:unit/@a+b.c_d-1 test: npm run test:unit/@a+b.c_d-1"}) {
		t.Fatalf("tasks = %q", got)
	}
}

func TestMakefileTargets(t *testing.T) {
	makefile := strings.Join([]string{
		".PHONY: test build",
		"CC := gcc",
		"FLAGS = a:b",
		"X ::= y",
		"Y ?= z",
		"build: main.o util.o ## the binary",
		"\tcc -o app main.o",
		"%.o: %.c",
		"\t$(CC) -c $<",
		"test lint:",
		"test: build",
		"install:: build",
		"debug: CFLAGS = -g",
		"$(BIN): main.o",
		"weird;name: x",
		".hidden:",
		"# comment: yes",
		"define RECIPE",
		"inside: define",
		"endef",
		"deps: a \\",
		"  b: c",
		"clean:",
		"",
	}, "\n")
	got := commands(list(t, project(t, map[string]string{"Makefile": makefile})))
	want := []string{
		"Makefile test test: make test",
		"Makefile build build: make build",
		"Makefile lint lint: make lint",
		"Makefile install other: make install",
		"Makefile deps other: make deps",
		"Makefile clean other: make clean",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tasks = %q\nwant %q", got, want)
	}
	payload := list(t, project(t, map[string]string{"GNUmakefile": "gnu:\n"}))
	if len(payload.Tasks) != 1 || payload.Tasks[0].Name != "gnu" {
		t.Fatalf("GNUmakefile = %#v", payload)
	}
}

func TestCargoAndGoDefaults(t *testing.T) {
	got := commands(list(t, project(t, map[string]string{"Cargo.toml": "[package]\n", "go.mod": "module x\n"})))
	want := []string{
		"Cargo.toml test test: cargo test",
		"go.mod test test: go test ./...",
		"Cargo.toml run dev: cargo run",
		"Cargo.toml build build: cargo build",
		"go.mod build build: go build ./...",
		"Cargo.toml clippy lint: cargo clippy",
		"go.mod vet lint: go vet ./...",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tasks = %q\nwant %q", got, want)
	}
}

func TestKindsOrderAndCap(t *testing.T) {
	var scripts []string
	for i := range 60 {
		scripts = append(scripts, fmt.Sprintf(`"misc%02d":"x"`, i))
	}
	scripts = append(scripts, `"format":"x"`, `"build:web":"x"`, `"start-api":"x"`, `"e2e":"x"`, `"typecheck":"x"`,
		`"compile":"x"`, `"dev":"x"`, `"test":"x"`, `"cy:spec":"x"`, `"testing":"x"`, `"preview":"x"`)
	payload := list(t, project(t, map[string]string{"package.json": `{"scripts":{` + strings.Join(scripts, ",") + `}}`}))
	if len(payload.Tasks) != protocol.MaxProjectTasks {
		t.Fatalf("%d tasks", len(payload.Tasks))
	}
	var head []string
	for _, task := range payload.Tasks[:11] {
		head = append(head, task.Name+"="+string(task.Kind))
	}
	want := []string{"e2e=test", "test=test", "cy:spec=test", "start-api=dev", "dev=dev", "preview=dev",
		"build:web=build", "compile=build", "format=lint", "typecheck=lint", "misc00=other"}
	if !slices.Equal(head, want) {
		t.Fatalf("order = %q\nwant %q", head, want)
	}
	if last := payload.Tasks[49].Name; last != "misc39" {
		t.Fatalf("last kept = %q, want misc39: 10 useful tasks, then the first 40 others", last)
	}
	if _, err := protocol.DecodeHost(envelope(t, payload)); err != nil {
		t.Fatalf("payload fails protocol validation: %v", err)
	}
}

func TestSymlinkedFilesAreIgnored(t *testing.T) {
	outside := project(t, map[string]string{"package.json": `{"scripts":{"dev":"x"}}`, "Makefile": "leak:\n"})
	dir := t.TempDir()
	for _, name := range []string{"package.json", "Makefile"} {
		if err := os.Symlink(filepath.Join(outside, name), filepath.Join(dir, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	if payload := list(t, dir); payload.State != protocol.TasksStateNone || len(payload.Tasks) != 0 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestOversizedFilesAreCapped(t *testing.T) {
	padding := strings.Repeat("# padding\n", maxFileBytes/10)
	payload := list(t, project(t, map[string]string{"Makefile": "first:\n" + padding + "late:\n"}))
	if got := commands(payload); !slices.Equal(got, []string{"Makefile first other: make first"}) {
		t.Fatalf("tasks = %q", got)
	}
	big := `{"scripts":{"dev":"x"},"pad":"` + strings.Repeat("a", maxFileBytes) + `"}`
	if payload := list(t, project(t, map[string]string{"package.json": big})); payload.State != protocol.TasksStateUnavailable {
		t.Fatalf("oversized package.json = %#v", payload)
	}
}

func TestInvalidJSONAndNone(t *testing.T) {
	payload := list(t, project(t, map[string]string{"package.json": `{"scripts":`}))
	if payload.State != protocol.TasksStateUnavailable || len(payload.Tasks) != 0 {
		t.Fatalf("broken package.json = %#v", payload)
	}
	// A broken package.json is skipped; the other sources still count.
	payload = list(t, project(t, map[string]string{"package.json": `{`, "go.mod": "module x\n"}))
	if payload.State != protocol.TasksStateReady || len(payload.Tasks) != 3 {
		t.Fatalf("broken package.json with go.mod = %#v", payload)
	}
	payload = list(t, project(t, map[string]string{"package.json": `{"name":"x"}`}))
	if payload.State != protocol.TasksStateReady || len(payload.Tasks) != 0 {
		t.Fatalf("no scripts = %#v", payload)
	}
	dir := project(t, map[string]string{"README.md": "hi"})
	if err := os.Mkdir(filepath.Join(dir, "go.mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if payload := list(t, dir); payload.State != protocol.TasksStateNone || len(payload.Tasks) != 0 {
		t.Fatalf("none = %#v", payload)
	}
	missing := filepath.Join(dir, "missing")
	if payload := List(context.Background(), missing); payload.State != protocol.TasksStateUnavailable || payload.Tasks == nil {
		t.Fatalf("missing folder = %#v", payload)
	}
}

func envelope(t *testing.T, payload protocol.TasksListedPayload) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	session, generation := "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	raw, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: protocol.TasksListed,
		RequestID: "11111111-1111-4111-8111-111111111111", SessionID: &session, Generation: &generation, Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
