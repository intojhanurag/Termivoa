package gatewayclient

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func fixtureCreate() protocol.HTTPSessionCreateRequest {
	return protocol.HTTPSessionCreateRequest{OperationID: testOperation, ProjectID: testProject, ShellProfileID: testProfile}
}

func fixtureAuth(t *testing.T, request protocol.GatewayRPCEnvelope) []byte {
	return reply(t, request.RequestID, protocol.GatewayAuthCheckResult, protocol.GatewayRPCPrincipalPayload{
		CredentialID: testCredID, DeviceID: testDevice, DeviceLabel: "Fixture phone", ExpiresAt: "2026-10-27T00:00:00Z",
	})
}

func fixtureRefused(t *testing.T, request protocol.GatewayRPCEnvelope) []byte {
	return reply(t, request.RequestID, protocol.GatewayError, protocol.BrokerRPCErrorPayload{
		Code: protocol.LimitExceeded, Message: "fixture action completed",
	})
}

// Cover every slow handler newly isolated from the shared read connection.
func TestSlowRPCDoesNotBlockAuthentication(t *testing.T) {
	calls := []struct {
		name string
		kind protocol.GatewayRPCMessageType
		call func(context.Context, *Client) error
	}{
		{"create", protocol.GatewaySessionCreate, func(ctx context.Context, client *Client) error {
			_, err := client.CreateSession(ctx, testCredential, fixtureCreate())
			return err
		}},
		{"terminate", protocol.GatewaySessionTerminate, func(ctx context.Context, client *Client) error {
			_, err := client.TerminateSession(ctx, testCredential, testSession, protocol.HTTPSessionTerminateRequest{OperationID: testOperation, Generation: testGeneration})
			return err
		}},
		{"push test", protocol.GatewayPushTest, func(ctx context.Context, client *Client) error {
			_, err := client.PushTest(ctx, testCredential)
			return err
		}},
		{"provider open", protocol.GatewayProvidersOpen, func(ctx context.Context, client *Client) error {
			_, err := client.OpenProvider(ctx, testCredential, "herdr", protocol.HTTPProviderOpenRequest{TargetID: "w1:p1.term_1", Columns: 80, Rows: 24})
			return err
		}},
	}
	for _, test := range calls {
		t.Run(test.name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			client, _ := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
				if request.Type == protocol.GatewayAuthCheck {
					return fixtureAuth(t, request)
				}
				if request.Type != test.kind {
					t.Errorf("unexpected request %s", request.Type)
					return nil
				}
				if request.Origin == nil || *request.Origin != testOrigin {
					t.Errorf("slow exchange lost forwarded origin: %v", request.Origin)
				}
				close(entered)
				<-release
				return fixtureRefused(t, request)
			})
			finished := make(chan error, 1)
			go func() {
				finished <- test.call(gatewayapi.WithForwardedOrigin(context.Background(), testOrigin), client)
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("slow request did not start")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			if _, err := client.Authenticate(ctx, testCredential); err != nil {
				t.Fatalf("authentication waited: %v", err)
			}
			once.Do(func() { close(release) })
			var remote *gatewayapi.Error
			if err := <-finished; !errors.As(err, &remote) || errors.Is(err, ErrDeliveryUncertain) {
				t.Fatalf("correlated remote refusal = %v", err)
			}
		})
	}
}

func TestSlowRPCLimitLeavesAuthenticationAvailableAndReleasesSlots(t *testing.T) {
	entered, release := make(chan struct{}, maxConcurrentSlowRPC), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	client, dials := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
		if request.Type == protocol.GatewayAuthCheck {
			return fixtureAuth(t, request)
		}
		entered <- struct{}{}
		<-release
		return reply(t, request.RequestID, protocol.GatewaySessionCreateResult,
			protocol.HTTPSessionMutationResponse{OperationID: testOperation, Session: testRecord()})
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan error, maxConcurrentSlowRPC)
	for range maxConcurrentSlowRPC {
		go func() { _, err := client.CreateSession(ctx, testCredential, fixtureCreate()); completed <- err }()
	}
	for range maxConcurrentSlowRPC {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("slow call did not enter")
		}
	}
	_, err := client.CreateSession(context.Background(), testCredential, fixtureCreate())
	var remote *gatewayapi.Error
	if !errors.As(err, &remote) || remote.Code != protocol.LimitExceeded || errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("request beyond the bound = %v", err)
	}
	if dials.Load() != maxConcurrentSlowRPC {
		t.Fatalf("dials beyond bound = %d", dials.Load())
	}
	authCtx, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer stop()
	if _, err := client.Authenticate(authCtx, testCredential); err != nil {
		t.Fatalf("auth while all slots occupied = %v", err)
	}
	cancel()
	for range maxConcurrentSlowRPC {
		select {
		case err := <-completed:
			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrDeliveryUncertain) {
				t.Fatalf("canceled delivered mutation = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled slow call did not return")
		}
	}
	once.Do(func() { close(release) })
	if _, err := client.CreateSession(context.Background(), testCredential, fixtureCreate()); err != nil {
		t.Fatalf("slot was not released after cancellation: %v", err)
	}
	if dials.Load() != maxConcurrentSlowRPC+2 {
		t.Fatalf("dials=%d; unexpected mutation retry", dials.Load())
	}
}

func TestCloseInterruptsActiveSlowRPC(t *testing.T) {
	entered := make(chan struct{}, 1)
	client, _ := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte { entered <- struct{}{}; return nil })
	finished := make(chan error, 1)
	go func() {
		_, err := client.CreateSession(context.Background(), testCredential, fixtureCreate())
		finished <- err
	}()
	<-entered
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, ErrDeliveryUncertain) {
			t.Fatalf("closed delivered mutation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt slow RPC")
	}
	if _, err := client.CreateSession(context.Background(), testCredential, fixtureCreate()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("request after Close = %v", err)
	}
}

func TestCloseInterruptsSlowRPCDial(t *testing.T) {
	entered := make(chan struct{})
	var dials atomic.Int32
	client := newClient(func(ctx context.Context) (net.Conn, error) {
		dials.Add(1)
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	finished := make(chan error, 1)
	go func() {
		_, err := client.CreateSession(context.Background(), testCredential, fixtureCreate())
		finished <- err
	}()
	<-entered
	_ = client.Close()
	select {
	case err := <-finished:
		if err == nil || errors.Is(err, ErrDeliveryUncertain) {
			t.Fatalf("closed undelivered dial = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not interrupt pending dial")
	}
	if dials.Load() != 1 {
		t.Fatal("dial was retried")
	}
}

// holdSlots starts n calls and waits until each one blocks in the broker.
func holdSlots(t *testing.T, n int, call func(*Client) error, client *Client, entered chan struct{}) {
	t.Helper()
	for range n {
		go func() { _ = call(client) }()
	}
	for range n {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("slow call did not enter")
		}
	}
}

// A dev-server page loads many modules; each proxied request checks the
// target, so the check must not need a slow slot.
func TestPreviewCheckWhileSlowSlotsAreFull(t *testing.T) {
	entered, release := make(chan struct{}, maxConcurrentSlowRPC), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	client, _ := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
		if request.Type == protocol.GatewayPreviewCheck {
			return reply(t, request.RequestID, protocol.GatewayPreviewCheckResult, protocol.GatewayRPCPreviewTargetPayload{Host: "127.0.0.1"})
		}
		entered <- struct{}{}
		<-release
		return fixtureRefused(t, request)
	})
	holdSlots(t, maxConcurrentSlowRPC, func(client *Client) error {
		_, err := client.CreateSession(context.Background(), testCredential, fixtureCreate())
		return err
	}, client, entered)
	for range 3 * maxConcurrentSlowRPC {
		if _, err := client.PreviewTarget(context.Background(), testCredential, testSession, 3000); err != nil {
			t.Fatalf("preview check while slow slots are full = %v", err)
		}
	}
}

// GitHub and herdr calls have their own pool, so they cannot starve session
// create; their own pool stays bounded.
func TestIntegrationCallsDoNotBlockSessionCreate(t *testing.T) {
	entered, release := make(chan struct{}, maxConcurrentIntegrationRPC), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	client, _ := pipeClient(t, func(_ int, request protocol.GatewayRPCEnvelope) []byte {
		if request.Type == protocol.GatewaySessionCreate {
			return reply(t, request.RequestID, protocol.GatewaySessionCreateResult,
				protocol.HTTPSessionMutationResponse{OperationID: testOperation, Session: testRecord()})
		}
		entered <- struct{}{}
		<-release
		return fixtureRefused(t, request)
	})
	holdSlots(t, maxConcurrentIntegrationRPC, func(client *Client) error {
		_, err := client.GitHubAction(context.Background(), testCredential, protocol.HTTPGitHubRequest{Action: protocol.Connect})
		return err
	}, client, entered)
	item, name := "w3", "silver-meadow"
	_, err := client.ManageProvider(context.Background(), testCredential, "herdr",
		protocol.HTTPProviderManageRequest{Action: protocol.CloseItem, ItemID: &item, Name: &name})
	var remote *gatewayapi.Error
	if !errors.As(err, &remote) || remote.Code != protocol.LimitExceeded {
		t.Fatalf("integration call beyond its bound = %v", err)
	}
	for range maxConcurrentSlowRPC + 1 {
		if _, err := client.CreateSession(context.Background(), testCredential, fixtureCreate()); err != nil {
			t.Fatalf("session create while integration slots are full = %v", err)
		}
	}
}
