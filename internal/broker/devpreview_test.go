package broker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// portProcess is a stream process whose tree "listens" on test-set ports.
type portProcess struct {
	*streamProcess
	mu    sync.Mutex
	ports []ptyhost.PortTarget
	scans int
}

func (process *portProcess) ListeningPorts(context.Context) ([]ptyhost.PortTarget, error) {
	process.mu.Lock()
	defer process.mu.Unlock()
	process.scans++
	return append([]ptyhost.PortTarget(nil), process.ports...), nil
}

func (process *portProcess) listen(ports ...ptyhost.PortTarget) {
	process.mu.Lock()
	process.ports = ports
	process.mu.Unlock()
}

func newPreviewFixture(t *testing.T) (*managementFixture, *Session, *portProcess, *auth.Service) {
	t.Helper()
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	process := &portProcess{streamProcess: &streamProcess{viewerProcess: newViewerProcess()}}
	var session *Session
	fixture.registry.config.start = func(parent context.Context, config SessionConfig) (registrySession, error) {
		config.InputQueueCapacity = DefaultInputQueueCapacity
		config.PTYOutputQueue = DefaultPTYOutputQueue
		ctx, cancel := context.WithCancel(context.Background())
		core, err := terminal.New(ctx, terminal.Config{Columns: config.Process.Columns, Rows: config.Process.Rows})
		if err != nil {
			cancel()
			return nil, err
		}
		session, err = startSessionRuntime(ctx, cancel, parent.Done(), config, process, core)
		return session, err
	}
	if _, err := fixture.server.createSession(context.Background(), protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: "86666666-6666-4666-8666-666666666667", ProjectID: rpcProjectID, ShellProfileID: rpcProfileID,
	}); err != nil {
		t.Fatal(err)
	}
	return fixture, session, process, service
}

func TestMergePreviewsKeepsFirstSeen(t *testing.T) {
	first, later := time.Unix(100, 0), time.Unix(200, 0)
	current, changed := mergePreviews(nil, []ptyhost.PortTarget{{Port: 5173, Host: "127.0.0.1"}}, first)
	if !changed || len(current) != 1 || !current[0].since.Equal(first) {
		t.Fatalf("first = %#v, %v", current, changed)
	}
	next, changed := mergePreviews(current, []ptyhost.PortTarget{{Port: 3000, Host: "::1"}, {Port: 5173, Host: "127.0.0.1"}}, later)
	if !changed || len(next) != 2 || next[0].port != 3000 || !next[0].since.Equal(later) || !next[1].since.Equal(first) {
		t.Fatalf("added = %#v, %v", next, changed)
	}
	if same, changed := mergePreviews(next, []ptyhost.PortTarget{{Port: 3000}, {Port: 5173}}, later.Add(time.Hour)); changed || !same[1].since.Equal(first) {
		t.Fatalf("unchanged = %#v, %v", same, changed)
	}
	if gone, changed := mergePreviews(next, nil, later); !changed || len(gone) != 0 || previewPayload(gone) != nil {
		t.Fatalf("closed = %#v, %v", gone, changed)
	}
}

// Output triggers a bounded OS scan; found ports reach presence and the
// gateway list, and a later scan drops a closed port.
func TestPreviewPortsFollowOutputAndReachTheList(t *testing.T) {
	fixture, session, process, service := newPreviewFixture(t)
	phone := pairDevice(t, service, "Pocket")
	process.listen(ptyhost.PortTarget{Port: 5173, Host: "127.0.0.1"})
	process.Emit([]byte("VITE ready  http://localhost:5173/\r\n"))
	presence := waitPresence(t, session, func(presence SessionPresence) bool { return len(presence.Previews) == 1 })
	if presence.Previews[0].Port != 5173 {
		t.Fatalf("previews = %#v", presence.Previews)
	}
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	response := gateway.gateway(t, protocol.GatewaySessionList, credentialPayload(phone.Credential))
	list, err := protocol.DecodeRPCPayload[protocol.HTTPSessionListResponse](response)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range list.Sessions {
		// The fixture's other session has no port lister and never has previews.
		if want := record.ID == session.id; want != (len(record.Previews) == 1 && record.Previews[0].Port == 5173) {
			t.Fatalf("record %s previews = %#v", record.ID, record.Previews)
		}
	}

	process.listen()
	process.Emit([]byte("stopped\r\n"))
	waitPresence(t, session, func(presence SessionPresence) bool { return presence.Previews == nil })
}

// gateway.preview.check re-authorizes the credential and allows only a port
// the session's own processes listen on.
func TestGatewayPreviewCheckAllowsOnlyOwnedPorts(t *testing.T) {
	fixture, session, process, service := newPreviewFixture(t)
	phone := pairDevice(t, service, "Pocket")
	process.listen(ptyhost.PortTarget{Port: 5173, Host: "::1"})
	gateway := startRPCTestClient(t, fixture.server, protocol.Gateway)
	check := func(credential, sessionID string, port int64) protocol.GatewayRPCEnvelope {
		return gateway.gateway(t, protocol.GatewayPreviewCheck, protocol.GatewayRPCPreviewCheckPayload{
			Credential: credential, SessionID: sessionID, Port: port,
		})
	}
	response := check(phone.Credential, session.id, 5173)
	target, err := protocol.DecodeRPCPayload[protocol.GatewayRPCPreviewTargetPayload](response)
	if err != nil || target.Host != "::1" {
		t.Fatalf("owned port = %#v %#v, %v", response, target, err)
	}
	if code := gatewayErrorCode(t, check(phone.Credential, session.id, 5432)); code != protocol.PreviewMissing {
		t.Fatalf("foreign port code = %s", code)
	}
	if code := gatewayErrorCode(t, check(phone.Credential, "8fffffff-ffff-4fff-8fff-ffffffffffff", 5173)); code != protocol.SessionMissing {
		t.Fatalf("unknown session code = %s", code)
	}
	if code := gatewayErrorCode(t, check(validBrowserSecret(t), session.id, 5173)); code != protocol.Unauthorized {
		t.Fatalf("unknown credential code = %s", code)
	}
	if _, err := service.RevokeDevice(context.Background(), phone.DeviceID); err != nil {
		t.Fatal(err)
	}
	if code := gatewayErrorCode(t, check(phone.Credential, session.id, 5173)); code != protocol.ErrorCodeRevoked {
		t.Fatalf("revoked credential code = %s", code)
	}
}

func validBrowserSecret(t *testing.T) string {
	t.Helper()
	secret, err := auth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	value, err := secret.TransportValue()
	if err != nil {
		t.Fatal(err)
	}
	return value
}
