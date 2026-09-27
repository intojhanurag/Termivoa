//go:build darwin || linux

package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
)

// TestGatewayControlFitsWhenBrokerSocketFits reproduces a report that long
// macOS usernames can push the runtime directory close enough to the Unix
// socket path limit that the broker's own "<runtime>/broker.sock" still
// fits, but the gateway control endpoint no longer does, so `pterm gateway`
// fails to start even though the broker works fine. The control endpoint
// must stay inside the broker's private, already-hardened runtime
// directory (never a separate, less-restricted location) and its full
// socket path must never exceed the broker's own.
func TestGatewayControlFitsWhenBrokerSocketFits(t *testing.T) {
	// Darwin permits 103 pathname bytes plus the terminating NUL for a Unix
	// socket address (see internal/ipc's unixSocketPathMax); Linux allows a
	// few more, but the product targets one portable limit.
	const unixSocketPathMax = 103
	const brokerSuffix = "/broker.sock" // appended by ipc.platformEndpoint
	const runtimeSuffix = "/runtime"    // matches platform.pathsForDirectory

	root, err := os.MkdirTemp("/tmp", "pterm-gw-fit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	// ipc canonicalizes the runtime directory (resolving aliases such as
	// macOS's /tmp -> /private/tmp) before measuring socket paths, so the
	// padding below must be computed against that same canonical form.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	// Pad the runtime directory's parent so the broker's own socket path
	// lands right at the platform limit, mirroring a long-username home
	// directory. This is the exact boundary the bug report describes: the
	// broker fits, but the gateway control endpoint must too.
	target := unixSocketPathMax - len(brokerSuffix) - len(runtimeSuffix)
	if pad := target - len(root) - 1; pad > 0 { // -1 for the separator filepath.Join adds
		root = filepath.Join(root, strings.Repeat("x", pad))
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimeDir := filepath.Join(root, "runtime")

	brokerSocketPath := filepath.Join(runtimeDir, "broker.sock")
	if got := len(brokerSocketPath); got > unixSocketPathMax {
		t.Fatalf("test setup: broker socket path is %d bytes, want <= %d", got, unixSocketPathMax)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stop := make(chan struct{}, 1)
	control, err := ListenGatewayControl(ctx, runtimeDir, "fit-test", func() { stop <- struct{}{} })
	if err != nil {
		t.Fatalf("gateway control failed to start even though the broker socket fits (broker path %d bytes): %v",
			len(brokerSocketPath), err)
	}

	// The endpoint must sit inside the private, already-0700 runtime
	// directory, not some separate or less-restricted location.
	info, err := os.Lstat(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime directory mode = %04o, want 0700", info.Mode().Perm())
	}

	// The control endpoint must actually work end to end: dial, stop, and
	// confirm the endpoint is gone, all still within the padded path.
	go func() {
		<-stop
		_ = control.Close()
	}()
	if stopped, err := localclient.StopGateway(ctx, runtimeDir, "fit-test"); !stopped || err != nil {
		t.Fatalf("stop = %v, %v", stopped, err)
	}
}
