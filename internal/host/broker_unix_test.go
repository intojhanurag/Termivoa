//go:build darwin || linux

package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
)

func TestBrokerDataLockRemainsHeldThroughRegistryAndStoreCleanup(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pterm-host-lock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseCleanup) }) })

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan BrokerReady, 1)
	done := make(chan error, 1)
	config := BrokerConfig{
		DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "runtime-one"),
		HostBuild: "data-lock-test", Profile: DevelopmentProfile,
		Ready: func(state BrokerReady) { ready <- state },
		beforeRegistryClose: func() {
			close(cleanupStarted)
			<-releaseCleanup
		},
	}
	go func() { done <- RunBroker(ctx, config) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("broker stopped before ready: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("broker did not become ready")
	}

	cancel()
	select {
	case <-cleanupStarted:
	case err := <-done:
		t.Fatalf("broker skipped held cleanup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("broker did not enter cleanup")
	}

	replacement := config
	replacement.RuntimeDir = filepath.Join(root, "runtime-two")
	replacement.Ready = nil
	replacement.beforeRegistryClose = nil
	if err := RunBroker(context.Background(), replacement); !errors.Is(err, ipc.ErrBrokerAlreadyRunning) {
		t.Fatalf("replacement during old cleanup error = %v", err)
	}

	releaseOnce.Do(func() { close(releaseCleanup) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first broker shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first broker cleanup did not finish")
	}

	replacementCtx, cancelReplacement := context.WithCancel(context.Background())
	replacementReady := make(chan BrokerReady, 1)
	replacementDone := make(chan error, 1)
	replacement.Ready = func(state BrokerReady) { replacementReady <- state }
	go func() { replacementDone <- RunBroker(replacementCtx, replacement) }()
	select {
	case <-replacementReady:
		cancelReplacement()
	case err := <-replacementDone:
		t.Fatalf("replacement after cleanup stopped before ready: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("replacement did not acquire released data lock")
	}
	select {
	case err := <-replacementDone:
		if err != nil {
			t.Fatalf("replacement shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replacement did not stop")
	}
}
