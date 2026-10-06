//go:build darwin || linux

package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func setupDone(t *testing.T, h *harness) protocol.HTTPGitHubSetup {
	t.Helper()
	waitFor(t, "the set-up", func() bool {
		setup := h.service.State("a").Setup
		return setup != nil && setup.Phase != protocol.Cloning
	})
	return *h.service.State("a").Setup
}

func wantProblem(t *testing.T, setup protocol.HTTPGitHubSetup, want protocol.HTTPGitHubSetupProblem) {
	t.Helper()
	if setup.Phase != protocol.HTTPGitHubSetupPhaseFailed || setup.Problem == nil || *setup.Problem != want {
		t.Fatalf("setup %+v problem %v, want %s", setup, setup.Problem, want)
	}
}

func tempFolders(t *testing.T, home string) []string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(home, tempPrefix+"*"))
	return matches
}

func TestSetupBadName(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	for _, repo := range []string{"o/.ssh", "o/-x", "o/a.git", "o/A.GIT", "o.x/n", "o/../n", "o", "o/n/m", "../n"} {
		if _, err := h.service.Setup(context.Background(), "a", repo); err != nil {
			t.Fatal(err)
		}
		wantProblem(t, setupDone(t, h), protocol.BadName)
	}
	if fake.count("/repos/o/.ssh")+fake.count("/repos/o/a.git") != 0 {
		t.Fatal("GitHub was asked about a bad name")
	}
}

func TestSetupNotConnected(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, false, nil)
	if _, err := h.service.Setup(context.Background(), "a", "o/n"); !errors.Is(err, ErrNotConnected) {
		t.Fatal(err)
	}
}

func TestSetupNotFound(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	if _, err := h.service.Setup(context.Background(), "a", "o/missing"); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, setupDone(t, h), protocol.NotFound)
	fake.with(func() { fake.repoInfo["o/moved"] = map[string]any{"full_name": "other/moved", "size": 1} })
	if _, err := h.service.Setup(context.Background(), "a", "o/moved"); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, setupDone(t, h), protocol.NotFound)
}

func TestSetupNameTakenAndNoSpace(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	for _, name := range []string{"plain", "Cased", "other"} {
		fake.with(func() {
			fake.repoInfo["o/"+strings.ToLower(name)] = map[string]any{"full_name": "o/" + strings.ToLower(name), "size": 1}
		})
	}
	if err := os.Mkdir(filepath.Join(h.service.home, "plain"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(h.service.home, "Cased"), 0o700); err != nil {
		t.Fatal(err)
	}
	fakeRepo(t, filepath.Join(h.service.home, "other"), fake.server.URL+"/someone/other.git")
	for _, repo := range []string{"o/plain", "o/cased", "o/other"} {
		if _, err := h.service.Setup(context.Background(), "a", repo); err != nil {
			t.Fatal(err)
		}
		wantProblem(t, setupDone(t, h), protocol.NameTaken)
	}

	fake.with(func() { fake.repoInfo["o/big"] = map[string]any{"full_name": "o/big", "size": 1 << 20} })
	h.service.freeSpace = func(string) (uint64, error) { return 3<<30 + gib - 1, nil } // 1 GiB of repository needs 4 GiB
	if _, err := h.service.Setup(context.Background(), "a", "o/big"); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, setupDone(t, h), protocol.NoSpace)
	if len(tempFolders(t, h.service.home)) != 0 {
		t.Fatal("a temp folder was made")
	}
}

func TestSetupAlreadyHere(t *testing.T) {
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	fake.with(func() {
		fake.repoInfo["o/here"] = map[string]any{"full_name": "o/here", "size": 1}
		fake.repoInfo["o/saved"] = map[string]any{"full_name": "o/saved", "size": 1}
	})
	fakeRepo(t, filepath.Join(h.service.home, "here"), "git@127.0.0.1:O/here.git")
	if _, err := h.service.Setup(context.Background(), "a", "o/here"); err != nil {
		t.Fatal(err)
	}
	setup := setupDone(t, h)
	if setup.Phase != protocol.Finished || setup.Already == nil || !*setup.Already || setup.ProjectID == nil || *setup.ProjectID != "p1" {
		t.Fatalf("%+v", setup)
	}
	elsewhere := filepath.Join(t.TempDir(), "saved")
	fakeRepo(t, elsewhere, fake.server.URL+"/o/saved.git")
	h.projects.mu.Lock()
	h.projects.list = append(h.projects.list, Project{ID: "p7", Path: elsewhere})
	h.projects.mu.Unlock()
	if _, err := h.service.Setup(context.Background(), "a", "o/saved"); err != nil {
		t.Fatal(err)
	}
	setup = setupDone(t, h)
	if setup.Phase != protocol.Finished || setup.Already == nil || setup.ProjectID == nil || *setup.ProjectID != "p7" || len(h.projects.adds) != 1 {
		t.Fatalf("%+v adds %v", setup, h.projects.adds)
	}
}

func TestSetupTooManyProjects(t *testing.T) {
	fake := newFake(t)
	fake.addFixture(t, "octo", "hello")
	h := newHarness(t, fake, true, nil)
	for index := range protocol.MaxAdminProjects {
		h.projects.list = append(h.projects.list, Project{ID: "x", Path: "/nowhere/" + string(rune('a'+index%26))})
	}
	if _, err := h.service.Setup(context.Background(), "a", "octo/hello"); err != nil {
		t.Fatal(err)
	}
	wantProblem(t, setupDone(t, h), protocol.TooManyProjects)
}

func TestSetupClonesThroughTheHelper(t *testing.T) {
	fake := newFake(t)
	fake.addFixture(t, "octo", "hello")
	h := newHarness(t, fake, true, nil)
	response, err := h.service.Setup(context.Background(), "a", "Octo/Hello")
	if err != nil || response.Setup == nil || response.Setup.Phase != protocol.Cloning {
		t.Fatalf("%+v %v", response.Setup, err)
	}
	if _, err := h.service.Setup(context.Background(), "b", "octo/hello"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second set-up: %v", err)
	}
	setup := setupDone(t, h)
	if setup.Phase != protocol.Finished || setup.ProjectID == nil || setup.Already != nil || setup.LFS == nil || !*setup.LFS {
		t.Fatalf("%+v problem %v", setup, setup.Problem)
	}
	if setup.Percent == nil || *setup.Percent != 100 {
		t.Fatalf("percent %v", setup.Percent)
	}
	// git ran the helper with an empty temp HOME; the clone's helper value put the
	// broker's HOME back, so a production pterm finds its runtime folder.
	if got := readAll(t, filepath.Join(filepath.Dir(h.config.Pterm), "helper-home")); got != h.config.Home {
		t.Fatalf("helper HOME = %q, want %q", got, h.config.Home)
	}
	final := filepath.Join(h.service.home, "hello")
	if readAll(t, filepath.Join(final, "README")) != "hello\n" || len(tempFolders(t, h.service.home)) != 0 {
		t.Fatal("the clone is not in ~/hello")
	}
	if slices.Compare(h.projects.adds, []string{final}) != 0 {
		t.Fatalf("adds %v", h.projects.adds)
	}
	fake.with(func() {
		if fake.gitAuthSeen == 0 {
			t.Error("git never sent the token")
		}
	})

	env := gitEnv(t)
	configFile := filepath.Join(final, ".git", "config")
	key := "credential." + fake.server.URL + "/octo/hello.git.helper"
	helpers := strings.Split(strings.TrimSuffix(runTestGit(t, env, "/", "config", "--file", configFile, "--get-all", key), "\n"), "\n")
	if len(helpers) != 2 || helpers[0] != "" || helpers[1] != "!'"+h.config.Pterm+"' github-credential" {
		t.Fatalf("helper lines %q", helpers)
	}
	if name := runTestGit(t, env, "/", "config", "--file", configFile, "--get", "user.name"); name != "octo\n" {
		t.Fatalf("user.name %q", name)
	}
	if email := runTestGit(t, env, "/", "config", "--file", configFile, "--get", "user.email"); email != "42+octo@users.noreply.github.com\n" {
		t.Fatalf("user.email %q", email)
	}
	for _, key := range []string{"core.hookspath", "protocol.allow", "credential.helper"} {
		if strings.Contains(strings.ToLower(readAll(t, configFile)), key+" ") {
			t.Fatalf("%s was written to the clone", key)
		}
	}

	// A system helper that records what it gets: the reset line keeps the
	// token from it on a later fetch in the clone.
	dir := t.TempDir()
	logFile := filepath.Join(dir, "log")
	recorder := filepath.Join(dir, "recorder")
	if err := os.WriteFile(recorder, []byte("#!/bin/sh\n{ echo \"$@\"; cat; } >> '"+logFile+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	system := filepath.Join(dir, "system")
	if err := os.WriteFile(system, []byte("[credential]\n\thelper = !"+recorder+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fetchEnv := slices.DeleteFunc(env, func(entry string) bool { return strings.HasPrefix(entry, "GIT_CONFIG_NOSYSTEM=") })
	fetchEnv = append(fetchEnv, "GIT_CONFIG_SYSTEM="+system, "GIT_ALLOW_PROTOCOL=http:https")
	runTestGit(t, append(fetchEnv, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.http.allow", "GIT_CONFIG_VALUE_0=always"), final, "fetch", "-q", "origin")
	if _, err := os.Stat(logFile); !os.IsNotExist(err) {
		t.Fatalf("the system helper got: %q", readAll(t, logFile))
	}
	// Without the reset line the recorder is asked: the check above can fail.
	runTestGit(t, env, "/", "config", "--file", configFile, "--unset-all", key)
	runTestGit(t, env, "/", "config", "--file", configFile, "--add", key, "!'"+h.config.Pterm+"' github-credential")
	runTestGit(t, append(fetchEnv, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.http.allow", "GIT_CONFIG_VALUE_0=always"), final, "fetch", "-q", "origin")
	if _, err := os.Stat(logFile); err != nil {
		t.Fatal("the recorder was never asked, so the test proves nothing")
	}
}

func TestSetupRefusesAFolderMadeDuringTheClone(t *testing.T) {
	fake := newFake(t)
	fake.addFixture(t, "octo", "hello")
	block := make(chan struct{})
	fake.with(func() { fake.gitBlock = block })
	h := newHarness(t, fake, true, nil)
	if _, err := h.service.Setup(context.Background(), "a", "octo/hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the temp folder", func() bool { return len(tempFolders(t, h.service.home)) == 1 })
	theirs := filepath.Join(h.service.home, "hello")
	if err := os.Mkdir(theirs, 0o700); err != nil { // empty: os.Rename would replace it
		t.Fatal(err)
	}
	close(block)
	wantProblem(t, setupDone(t, h), protocol.NameTaken)
	if entries, err := os.ReadDir(theirs); err != nil || len(entries) != 0 {
		t.Fatalf("their folder changed: %v %v", entries, err)
	}
	if len(tempFolders(t, h.service.home)) != 0 {
		t.Fatal("the temp folder is still there")
	}
}

func TestSetupStopAndTimeoutLeaveNothing(t *testing.T) {
	for _, test := range []struct {
		name string
		want protocol.HTTPGitHubSetupProblem
	}{{"stop", protocol.Stopped}, {"timeout", protocol.Timeout}} {
		t.Run(test.name, func(t *testing.T) {
			fake := newFake(t)
			fake.addFixture(t, "octo", "hello")
			block := make(chan struct{})
			t.Cleanup(func() { close(block) })
			fake.with(func() { fake.gitBlock = block })
			h := newHarness(t, fake, true, func(_ *Config, opts *options) {
				if test.want == protocol.Timeout {
					opts.cloneLimit = 500 * time.Millisecond
				}
			})
			if _, err := h.service.Setup(context.Background(), "a", "octo/hello"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the temp folder", func() bool { return len(tempFolders(t, h.service.home)) == 1 })
			if test.want == protocol.Stopped {
				time.Sleep(100 * time.Millisecond)
				h.service.Stop("a")
			}
			wantProblem(t, setupDone(t, h), test.want)
			if len(tempFolders(t, h.service.home)) != 0 {
				t.Fatal("the temp folder is still there")
			}
			if _, err := os.Lstat(filepath.Join(h.service.home, "hello")); !os.IsNotExist(err) {
				t.Fatal("~/hello was made")
			}
		})
	}
}

func TestCloneCommandIsFixed(t *testing.T) {
	for _, name := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(name, "")
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/501")
	fake := newFake(t)
	h := newHarness(t, fake, true, nil)
	service := h.service
	service.opts = defaultOptions // the production list (no http)
	command := service.cloneCommand(context.Background(), "/opt/git/bin/git", "/tmp/gh", "octo", "hello", "/home/u/.termivoa-clone-test-1")
	// The clone's helper gets the broker's HOME and XDG_RUNTIME_DIR back: git runs with an empty temp HOME.
	helper := "!HOME='" + h.config.Home + "' XDG_RUNTIME_DIR='/run/user/501' '" + h.config.Pterm + "' github-credential"
	want := []string{
		"HOME=/tmp/gh", "PATH=/opt/git/bin:/usr/bin:/bin", "LC_ALL=C", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1",
		"GIT_CONFIG_COUNT=10",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.https://github.com/octo/hello.git.helper", "GIT_CONFIG_VALUE_1=" + helper,
		"GIT_CONFIG_KEY_2=protocol.allow", "GIT_CONFIG_VALUE_2=never",
		"GIT_CONFIG_KEY_3=protocol.https.allow", "GIT_CONFIG_VALUE_3=always",
		"GIT_CONFIG_KEY_4=http.followRedirects", "GIT_CONFIG_VALUE_4=false",
		"GIT_CONFIG_KEY_5=http.proxy", "GIT_CONFIG_VALUE_5=",
		"GIT_CONFIG_KEY_6=core.hooksPath", "GIT_CONFIG_VALUE_6=/dev/null",
		"GIT_CONFIG_KEY_7=core.fsmonitor", "GIT_CONFIG_VALUE_7=false",
		"GIT_CONFIG_KEY_8=submodule.recurse", "GIT_CONFIG_VALUE_8=false",
		"GIT_CONFIG_KEY_9=transfer.fsckObjects", "GIT_CONFIG_VALUE_9=true",
	}
	if slices.Compare(command.Env, want) != 0 {
		t.Fatalf("env\n%q\nwant\n%q", command.Env, want)
	}
	args := []string{"/opt/git/bin/git", "clone", "--no-recurse-submodules", "--template=", "--progress",
		"https://github.com/octo/hello.git", "/home/u/.termivoa-clone-test-1"}
	if slices.Compare(command.Args, args) != 0 || command.Dir != "/" {
		t.Fatalf("args %q dir %q", command.Args, command.Dir)
	}
	all := strings.Join(append(command.Env, command.Args...), "\n")
	if strings.Contains(all, fake.token()) || strings.Contains(all, "ghr_") || strings.Contains(all, "ghu_") {
		t.Fatal("a token is in the command")
	}
}

func TestHelperCommand(t *testing.T) {
	for _, test := range []struct{ pterm, profile, runtime, want string }{
		{"/usr/local/bin/pterm", "production", "/r", "!'/usr/local/bin/pterm' github-credential"},
		{"/Users/a b/pterm", "development", "/tmp/run dir", "!'/Users/a b/pterm' github-credential --dev --runtime-dir '/tmp/run dir'"},
	} {
		if got := helperCommand(test.pterm, test.profile, test.runtime); got != test.want {
			t.Errorf("%q", got)
		}
	}
}

func TestQuoteInAPathTurnsTheFeatureOff(t *testing.T) {
	for _, config := range []Config{
		{ClientID: "Iv1.x", Home: t.TempDir(), DataDir: t.TempDir(), Pterm: "/it's/pterm"},
		{ClientID: "Iv1.x", Home: t.TempDir(), DataDir: t.TempDir(), Pterm: "/bin/pterm", RuntimeDir: "/run/it's"},
	} {
		if service, err := newService(config, Deps{}, defaultOptions); service != nil || err != nil {
			t.Fatalf("%v %v", service, err)
		}
	}
}

func TestQuoteInAnXDGFolderTurnsTheFeatureOff(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/it's")
	config := Config{ClientID: "Iv1.x", Home: t.TempDir(), DataDir: t.TempDir(), Pterm: "/bin/pterm"}
	if service, err := newService(config, Deps{}, defaultOptions); service != nil || err != nil {
		t.Fatalf("%v %v", service, err)
	}
}

func TestDevProfileWritesDevHelper(t *testing.T) {
	fake := newFake(t)
	fake.addFixture(t, "octo", "hello")
	h := newHarness(t, fake, true, func(config *Config, _ *options) { config.Profile = "development" })
	if _, err := h.service.Setup(context.Background(), "a", "octo/hello"); err != nil {
		t.Fatal(err)
	}
	if setup := setupDone(t, h); setup.Phase != protocol.Finished {
		t.Fatalf("%+v %v", setup, setup.Problem)
	}
	configFile := filepath.Join(h.service.home, "hello", ".git", "config")
	key := "credential." + fake.server.URL + "/octo/hello.git.helper"
	got := runTestGit(t, gitEnv(t), "/", "config", "--file", configFile, "--get-all", key)
	if got != "\n!'"+h.config.Pterm+"' github-credential --dev --runtime-dir '/run/termivoa-test'\n" {
		t.Fatalf("%q", got)
	}
}

func TestProgressWriter(t *testing.T) {
	var seen []int64
	writer := &progressWriter{report: func(percent int64) { seen = append(seen, percent) }}
	for _, chunk := range []string{"Cloning into 'x'...\nremote: Counting objects: 50% (1/2)\r", "Receiving obj", "ects:  42% (5/12)\rReceiving objects: 100% (12/12), done.\n",
		strings.Repeat("x", 10000) + "Receiving objects: 7%\n", "Receiving objects: 999%\n"} {
		_, _ = writer.Write([]byte(chunk))
	}
	if slices.Compare(seen, []int64{42, 100}) != 0 || len(writer.piece) != 0 {
		t.Fatalf("%v", seen)
	}
}

func TestSweepRemovesOnlyThisProfilesTempFolders(t *testing.T) {
	fake := newFake(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	mine := filepath.Join(home, tempPrefix+"test-0123456789abcdef")
	theirs := filepath.Join(home, tempPrefix+"production-0123456789abcdef")
	for _, dir := range []string{filepath.Join(mine, "deep"), theirs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(home, tempPrefix+"test-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	newHarness(t, fake, false, func(config *Config, _ *options) { config.Home = home })
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatal("this profile's temp folder is still there")
	}
	for _, kept := range []string{theirs, file} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("%s was removed", kept)
		}
	}
}

func TestRenameFallsBackWithoutExclusiveRename(t *testing.T) {
	old := renameNoReplace
	t.Cleanup(func() { renameNoReplace = old })
	for _, unsupported := range []error{syscall.EINVAL, syscall.ENOSYS, syscall.ENOTSUP, syscall.EOPNOTSUPP} {
		renameNoReplace = func(string, string) error { return unsupported }
		dir := t.TempDir()
		from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
		if err := os.Mkdir(from, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := renameExclusive(from, to); err != nil {
			t.Fatalf("%v: %v", unsupported, err)
		}
		if err := os.Mkdir(from, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := renameExclusive(from, to); !errors.Is(err, syscall.EEXIST) {
			t.Fatalf("%v: an existing folder gave %v", unsupported, err)
		}
	}
	renameNoReplace = func(string, string) error { return syscall.EACCES }
	if err := renameExclusive("/a", "/b"); !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
	renameNoReplace = old
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "from"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "to"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := renameExclusive(filepath.Join(dir, "from"), filepath.Join(dir, "to")); !errors.Is(err, syscall.EEXIST) && !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("the system rename replaced an empty folder: %v", err)
	}
}
