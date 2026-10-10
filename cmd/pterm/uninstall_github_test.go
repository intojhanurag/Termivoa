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

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/lifecycle"
)

func uninstallGitHubFixture(t *testing.T) (lifecycleEnv, *int) {
	t.Helper()
	env, _ := newLifecycleTestEnv(t)
	if err := os.MkdirAll(env.paths.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(env.paths.DataDir, "github.json")
	// Synthetic saved pair only; this fixture never contacts GitHub.
	if err := os.WriteFile(path, []byte(`{"login":"fixture","accessToken":"fixture-access","refreshToken":"fixture-refresh"}`), 0600); err != nil {
		t.Fatal(err)
	}
	count := new(int)
	env.revokeGitHub = func(_ context.Context, dir string) (bool, bool, error) {
		*count++
		canonical, _ := filepath.EvalSymlinks(env.paths.DataDir)
		if dir != canonical {
			t.Fatalf("cleanup used unexpected directory")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("revocation ran after token deletion")
		}
		_, lock, err := ipc.LockBrokerDataDirectory(dir)
		if err == nil {
			lock.Close()
			t.Fatal("cleanup did not hold exclusive data ownership")
		}
		return true, true, nil
	}
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	return env, count
}

func TestUninstallGitHubRevokeBeforeDelete(t *testing.T) {
	env, calls := uninstallGitHubFixture(t)
	var out bytes.Buffer
	if err := runWithInput(context.Background(), []string{"uninstall", "--yes", "--delete-data"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("revoke calls=%d, want 1 before deletion", *calls)
	}
	assertOnlyOwnershipMarker(t, env.paths.DataDir)
	if !strings.Contains(out.String(), "revocation request accepted") || strings.Contains(out.String(), "login revoked") {
		t.Fatal("success must report request acceptance, not completed revocation")
	}
}
func TestUninstallGitHubConsentAndDataPreservation(t *testing.T) {
	for _, args := range [][]string{{"uninstall", "--dry-run", "--delete-data"}, {"uninstall", "--delete-data"}, {"uninstall", "--yes"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env, calls := uninstallGitHubFixture(t)
			var out bytes.Buffer
			runWithInput(context.Background(), args, nil, &out)
			if *calls != 0 {
				t.Fatal("revoke ran without consent or data deletion")
			}
			if _, err := os.Stat(filepath.Join(env.paths.DataDir, "github.json")); err != nil {
				t.Fatal("saved login changed")
			}
		})
	}
}
func TestUninstallGitHubFailureBehavior(t *testing.T) {
	for _, localFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote unconfirmed", true: "local failure"}[localFailure], func(t *testing.T) {
			env, _ := uninstallGitHubFixture(t)
			env.revokeGitHub = func(context.Context, string) (bool, bool, error) {
				if localFailure {
					return true, false, errors.New("fixture-secret-must-not-escape")
				}
				return true, false, nil
			}
			loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
			var out bytes.Buffer
			err := runWithInput(context.Background(), []string{"uninstall", "--yes", "--delete-data"}, nil, &out)
			if localFailure {
				if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(out.String(), "fixture-secret") {
					t.Fatalf("unsafe local failure: %v", err)
				}
				if _, err := os.Stat(filepath.Join(env.paths.DataDir, "github.json")); err != nil {
					t.Fatal("local failure discarded login")
				}
			} else {
				if err != nil || !strings.Contains(out.String(), githubRevokeHint) {
					t.Fatalf("remote failure lacked manual hint: %v", err)
				}
				assertOnlyOwnershipMarker(t, env.paths.DataDir)
			}
		})
	}
}

func assertOnlyOwnershipMarker(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 1 || entries[0].Name() != ipc.BrokerDataLockFile {
		t.Fatalf("remaining data entries=%v, err=%v", entries, err)
	}
}

func TestUninstallGitHubLockAndCanceledCleanupPreserveData(t *testing.T) {
	for _, mode := range []string{"held lock", "cancel after acceptance"} {
		t.Run(mode, func(t *testing.T) {
			env, calls := uninstallGitHubFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "held lock" {
				_, lock, err := ipc.LockBrokerDataDirectory(env.paths.DataDir)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			} else {
				env.revokeGitHub = func(context.Context, string) (bool, bool, error) {
					*calls++
					cancel()
					return true, false, context.Canceled
				}
				loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
			}
			var out bytes.Buffer
			if err := runWithInput(ctx, []string{"uninstall", "--yes", "--delete-data"}, nil, &out); err == nil {
				t.Fatal("unsafe cleanup succeeded")
			}
			if mode == "held lock" && *calls != 0 {
				t.Fatal("revoke ran without exclusive ownership")
			}
			if _, err := os.Stat(filepath.Join(env.paths.DataDir, "github.json")); err != nil {
				t.Fatal("failure discarded data")
			}
		})
	}
}

func TestUninstallGitHubParentAliasCannotDeleteLockNamespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GitHub uninstall cleanup is Unix-only")
	}
	env, calls := uninstallGitHubFixture(t)
	alias := filepath.Join(filepath.Dir(filepath.Dir(env.paths.DataDir)), "cache-alias")
	if err := os.Symlink(filepath.Dir(env.paths.DataDir), alias); err != nil {
		t.Fatal(err)
	}
	env.paths.RuntimeDir = filepath.Join(alias, filepath.Base(env.paths.DataDir), "runtime")
	loadLifecycleEnv = func() (lifecycleEnv, error) { return env, nil }
	var out bytes.Buffer
	if err := runWithInput(context.Background(), []string{"uninstall", "--yes", "--delete-data"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatal("expected one revoke")
	}
	assertOnlyOwnershipMarker(t, env.paths.DataDir)
}
func TestOfflineCleanupRejectsContainingRootsAndKeepsSymlinkTargets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("GitHub uninstall cleanup is Unix-only")
	}
	outer := filepath.Join(t.TempDir(), "Phone Terminal")
	data := filepath.Join(outer, "nested", "Phone Terminal")
	canonical, lock, err := ipc.LockBrokerDataDirectory(data)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := checkedOfflineCleanup([]lifecycle.Step{{RemoveTree: data}, {RemoveTree: outer}}, canonical); err == nil {
		t.Fatal("containing root accepted")
	}
	alias := filepath.Join(t.TempDir(), "Phone Terminal")
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatal(err)
	}
	steps, err := checkedOfflineCleanup([]lifecycle.Step{{RemoveTree: data}, {RemoveTree: alias}}, canonical)
	if err != nil || len(steps) != 2 || steps[1].RemoveTree != alias {
		t.Fatal("top-level symlink must remain unlink-only")
	}
	if err := lifecycle.Apply(context.Background(), nil, lifecycle.Plan{Steps: steps}, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertOnlyOwnershipMarker(t, canonical)
	_, replacement, err := ipc.LockBrokerDataDirectory(canonical)
	if err == nil {
		replacement.Close()
		t.Fatal("cleanup permitted replacement lock")
	}
}
