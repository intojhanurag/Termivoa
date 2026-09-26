package host

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestGatewayControlStopsOnlyOnRequestAndIsSingleInstance(t *testing.T) {
	parent := ""
	if runtime.GOOS == "darwin" {
		parent = "/tmp" // Keep the Unix socket path within the OS byte limit.
	}
	root, err := os.MkdirTemp(parent, "pterm-gateway-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	runtimeDir := filepath.Join(root, "runtime")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if stopped, err := localclient.StopGateway(ctx, runtimeDir, "control-test"); stopped || err != nil {
		t.Fatalf("stop without a gateway = %v, %v", stopped, err)
	}
	stop := make(chan struct{}, 1)
	control, err := ListenGatewayControl(ctx, runtimeDir, "control-test", func() { stop <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ListenGatewayControl(ctx, runtimeDir, "control-test", func() {}); err == nil {
		_ = second.Close()
		t.Fatal("a second gateway control endpoint started for the same runtime directory")
	}

	// Any other admin request is refused without stopping the gateway.
	admin, err := localclient.DialAdmin(ctx, localclient.GatewayControlDir(runtimeDir), "control-test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = admin.Shutdown(ctx)
	_ = admin.Close()
	var remote *localclient.RemoteError
	if !errors.As(err, &remote) || remote.Code != protocol.InvalidRequest {
		t.Fatalf("shutdown sent to the gateway = %v", err)
	}
	select {
	case <-stop:
		t.Fatal("a non-stop request stopped the gateway")
	default:
	}

	go func() {
		<-stop
		_ = control.Close() // What runGateway does once the gateway exits.
	}()
	if stopped, err := localclient.StopGateway(ctx, runtimeDir, "control-test"); !stopped || err != nil {
		t.Fatalf("stop = %v, %v", stopped, err)
	}
	if stopped, err := localclient.StopGateway(ctx, runtimeDir, "control-test"); stopped || err != nil {
		t.Fatalf("second stop = %v, %v", stopped, err)
	}
}
