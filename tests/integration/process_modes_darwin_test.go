//go:build darwin

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

const (
	processProjectID     = "a1111111-1111-4111-8111-111111111111"
	processProfileID     = "a2222222-2222-4222-8222-222222222222"
	processOperationID   = "a3333333-3333-4333-8333-333333333333"
	processRequestID     = "a4444444-4444-4444-8444-444444444444"
	processReplayRequest = "a5555555-5555-4555-8555-555555555555"
	processStatusRequest = "a6666666-6666-4666-8666-666666666666"
	processTerminateID   = "a7777777-7777-4777-8777-777777777777"
	processTerminateReq  = "a8888888-8888-4888-8888-888888888888"
	recoveryBrokerID     = "b1111111-1111-4111-8111-111111111111"
	recoverySessionID    = "b2222222-2222-4222-8222-222222222222"
	recoveryGenerationID = "b3333333-3333-4333-8333-333333333333"
	recoveryProjectID    = "b4444444-4444-4444-8444-444444444444"
	recoveryProfileID    = "b5555555-5555-4555-8555-555555555555"
)

func TestPtermBrokerAndGatewayExecutableModesRestartIndependently(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pterm-executable-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dataDir := filepath.Join(root, "data")
	runtimeDir := filepath.Join(root, "runtime")
	fixture := buildProcessFixture(t, root)
	seedSavedConfiguration(t, dataDir, fixture)
	pterm := filepath.Join(root, "pterm")
	build := exec.Command("go", "build", "-o", pterm, "../../cmd/pterm")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.1")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pterm: %v\n%s", err, output)
	}

	brokerProcess := startCommand(t, pterm,
		"__broker", "--dev", "--data-dir", dataDir, "--runtime-dir", runtimeDir)
	welcome := waitBrokerHandshake(t, runtimeDir)
	address := reserveLoopbackAddress(t)
	gatewayOne := startCommand(t, pterm,
		"__gateway", "--dev", "--runtime-dir", runtimeDir, "--listen", address)
	waitGatewayHealth(t, address, welcome.BrokerGeneration)
	created := createSession(t, runtimeDir, processRequestID)

	killCommand(t, gatewayOne, "first gateway executable")
	replayed := createSession(t, runtimeDir, processReplayRequest)
	if replayed.Session.ID != created.Session.ID || replayed.Session.Generation != created.Session.Generation {
		t.Fatalf("gateway-stop replay identity = %#v, want %#v", replayed.Session, created.Session)
	}
	status := sessionStatus(t, runtimeDir, created.Session.ID, created.Session.Generation)
	if status.State != protocol.Running {
		t.Fatalf("session state after gateway executable stop = %s", status.State)
	}

	gatewayTwo := startCommand(t, pterm,
		"__gateway", "--dev", "--runtime-dir", runtimeDir, "--listen", address)
	waitGatewayHealth(t, address, welcome.BrokerGeneration)
	terminated := terminateSession(t, runtimeDir, created.Session.ID, created.Session.Generation)
	if terminated.Session.State != protocol.Exiting {
		t.Fatalf("terminate state = %s", terminated.Session.State)
	}
	stopCommand(t, gatewayTwo, "second gateway executable")
	stopCommand(t, brokerProcess, "broker executable")
}

func TestGatewayRestartPreservesBrokerSessionAndCanonicalOutput(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pterm-process-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dataDir := filepath.Join(root, "data")
	runtimeDir := filepath.Join(root, "runtime")
	fixture := buildProcessFixture(t, root)
	seedSavedConfiguration(t, dataDir, fixture)

	brokerCtx, cancelBroker := context.WithCancel(context.Background())
	brokerReady := make(chan host.BrokerReady, 1)
	brokerDone := make(chan error, 1)
	brokerConfig := host.BrokerConfig{
		DataDir: dataDir, RuntimeDir: runtimeDir, HostBuild: "process-integration",
		Profile: host.DevelopmentProfile,
		Ready:   func(state host.BrokerReady) { brokerReady <- state },
	}
	go func() { brokerDone <- host.RunBroker(brokerCtx, brokerConfig) }()
	state := waitBrokerReady(t, brokerReady)
	welcome := waitBrokerHandshake(t, runtimeDir)
	if welcome.BrokerGeneration != state.Generation {
		t.Fatalf("handshake generation = %q, want %q", welcome.BrokerGeneration, state.Generation)
	}

	gatewayOneCtx, cancelGatewayOne := context.WithCancel(context.Background())
	gatewayOneReady := make(chan gateway.ReadyState, 1)
	gatewayOneDone := make(chan error, 1)
	go func() {
		gatewayOneDone <- gateway.Run(gatewayOneCtx, gateway.Config{
			RuntimeDir: runtimeDir, HostBuild: "gateway-integration", ListenAddress: "127.0.0.1:0",
			Profile:       host.DevelopmentProfile,
			ProbeInterval: 25 * time.Millisecond,
			Ready:         func(state gateway.ReadyState) { gatewayOneReady <- state },
		})
	}()
	gatewayOne := waitGatewayReady(t, gatewayOneReady)
	if gatewayOne.BrokerGeneration != state.Generation {
		t.Fatalf("first gateway generation = %q, want %q", gatewayOne.BrokerGeneration, state.Generation)
	}

	created := createSession(t, runtimeDir, processRequestID)
	managed, err := state.Registry.Get(created.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := managed.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Generation != created.Session.Generation || status.PID <= 0 {
		t.Fatalf("created session status = %#v, response = %#v", status, created.Session)
	}
	before := waitForCounterFrame(t, managed, 0)

	cancelGatewayOne()
	waitProcessResult(t, gatewayOneDone, "first gateway")
	select {
	case <-managed.(interface{ Done() <-chan struct{} }).Done():
		t.Fatal("gateway shutdown ended the broker-owned session")
	default:
	}
	statusAfterStop, err := managed.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if statusAfterStop.PID != status.PID || statusAfterStop.Generation != status.Generation ||
		statusAfterStop.State != broker.SessionRunning {
		t.Fatalf("session changed after gateway stop: before %#v, after %#v", status, statusAfterStop)
	}
	after := waitForCounterFrame(t, managed, before.Revision)
	if after.Revision <= before.Revision || frameText(after) == frameText(before) {
		t.Fatalf("canonical output did not advance: revisions %d -> %d", before.Revision, after.Revision)
	}

	secondBrokerCtx, cancelSecondBroker := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecondBroker()
	secondBrokerConfig := brokerConfig
	secondBrokerConfig.RuntimeDir = filepath.Join(root, "second-runtime")
	secondBrokerConfig.Ready = nil
	if err := host.RunBroker(secondBrokerCtx, secondBrokerConfig); !errors.Is(err, ipc.ErrBrokerAlreadyRunning) {
		t.Fatalf("second broker error = %v", err)
	}
	statusAfterSecondBroker, err := managed.Status(context.Background())
	if err != nil || statusAfterSecondBroker.State != broker.SessionRunning {
		t.Fatalf("second broker attempt changed live session: %#v, %v", statusAfterSecondBroker, err)
	}
	persistedAfterSecondBroker, err := state.Store.GetSession(context.Background(), created.Session.ID)
	if err != nil || persistedAfterSecondBroker.State != store.SessionRunning ||
		persistedAfterSecondBroker.BrokerGeneration != state.Generation {
		t.Fatalf("second broker attempt changed live metadata: %#v, %v", persistedAfterSecondBroker, err)
	}

	gatewayTwoCtx, cancelGatewayTwo := context.WithCancel(context.Background())
	gatewayTwoReady := make(chan gateway.ReadyState, 1)
	gatewayTwoDone := make(chan error, 1)
	go func() {
		gatewayTwoDone <- gateway.Run(gatewayTwoCtx, gateway.Config{
			RuntimeDir: runtimeDir, HostBuild: "gateway-integration", ListenAddress: "127.0.0.1:0",
			Profile:       host.DevelopmentProfile,
			ProbeInterval: 25 * time.Millisecond,
			Ready:         func(state gateway.ReadyState) { gatewayTwoReady <- state },
		})
	}()
	gatewayTwo := waitGatewayReady(t, gatewayTwoReady)
	if gatewayTwo.BrokerGeneration != state.Generation {
		t.Fatalf("restarted gateway generation = %q, want %q", gatewayTwo.BrokerGeneration, state.Generation)
	}
	assertGatewayHealth(t, gatewayTwo.Address, state.Generation)
	replayed := createSession(t, runtimeDir, processReplayRequest)
	if replayed.Session.ID != created.Session.ID || replayed.Session.Generation != created.Session.Generation {
		t.Fatalf("replayed create identity = %#v, want %#v", replayed.Session, created.Session)
	}
	statusAfterRestart, err := managed.Status(context.Background())
	if err != nil || statusAfterRestart.PID != status.PID {
		t.Fatalf("gateway restart changed child identity: %#v, %v", statusAfterRestart, err)
	}
	waitForCounterFrame(t, managed, after.Revision)

	cancelGatewayTwo()
	waitProcessResult(t, gatewayTwoDone, "second gateway")
	cancelBroker()
	waitProcessResult(t, brokerDone, "broker")
}

func TestBrokerStartupMarksPriorLiveMetadataLostWithoutRerun(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pterm-recovery-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	dataDir := filepath.Join(root, "data")
	runtimeDir := filepath.Join(root, "runtime")
	seedPriorLiveSession(t, dataDir)

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan host.BrokerReady, 1)
	done := make(chan error, 1)
	go func() {
		done <- host.RunBroker(ctx, host.BrokerConfig{
			DataDir: dataDir, RuntimeDir: runtimeDir, HostBuild: "recovery-integration",
			Profile: host.DevelopmentProfile,
			Ready:   func(state host.BrokerReady) { ready <- state },
		})
	}()
	state := waitBrokerReady(t, ready)
	if len(state.Recovered) != 1 || state.Recovered[0].ID != recoverySessionID ||
		state.Recovered[0].State != store.SessionLost {
		t.Fatalf("recovered sessions = %#v", state.Recovered)
	}
	persisted, err := state.Store.GetSession(context.Background(), recoverySessionID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.State != store.SessionLost || persisted.EndedAt == nil || persisted.ExitCode != nil {
		t.Fatalf("persisted recovery state = %#v", persisted)
	}
	statuses, err := state.Registry.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 0 {
		t.Fatalf("broker silently reran prior sessions: %#v", statuses)
	}
	waitBrokerHandshake(t, runtimeDir)
	cancel()
	waitProcessResult(t, done, "recovery broker")
}

func buildProcessFixture(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "ptychild")
	command := exec.Command("go", "build", "-o", path, "../fixtures/ptychild")
	command.Env = append(os.Environ(), "GOTOOLCHAIN=go1.27.1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	return path
}

func seedSavedConfiguration(t *testing.T, dataDir, fixture string) {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	persistence, err := store.Open(context.Background(), filepath.Join(dataDir, "phone-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	if _, err := persistence.BeginBrokerGeneration(context.Background(), recoveryBrokerID, now); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutProject(context.Background(), store.Project{
		ID: processProjectID, DisplayName: "Counter", CanonicalPath: dataDir,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutShellProfile(context.Background(), store.ShellProfile{
		ID: processProfileID, DisplayName: "fixture", Platform: store.PlatformDarwin,
		ExecutablePath: fixture, Arguments: []string{"counter"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}
}

func seedPriorLiveSession(t *testing.T, dataDir string) {
	t.Helper()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	persistence, err := store.Open(context.Background(), filepath.Join(dataDir, "phone-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	if _, err := persistence.BeginBrokerGeneration(context.Background(), recoveryBrokerID, now); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutProject(context.Background(), store.Project{
		ID: recoveryProjectID, DisplayName: "Prior", CanonicalPath: dataDir,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutShellProfile(context.Background(), store.ShellProfile{
		ID: recoveryProfileID, DisplayName: "prior", Platform: store.PlatformDarwin,
		ExecutablePath: "/bin/zsh", Arguments: []string{"-l"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	metadata := store.SessionMetadata{
		ID: recoverySessionID, Generation: recoveryGenerationID, BrokerGeneration: recoveryBrokerID,
		Title: "Prior", ProjectID: recoveryProjectID, ShellProfileID: recoveryProfileID,
		State: store.SessionStarting, Columns: 80, Rows: 24, CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateSession(context.Background(), metadata); err != nil {
		t.Fatal(err)
	}
	if err := persistence.TransitionSession(
		context.Background(), recoverySessionID, recoveryGenerationID,
		store.SessionRunning, nil, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}
}

func createSession(t *testing.T, runtimeDir, requestID string) protocol.BrokerRPCSessionMutationResultPayload {
	t.Helper()
	payload, _ := json.Marshal(protocol.BrokerRPCSessionCreateRequestPayload{
		OperationID: processOperationID, ProjectID: processProjectID, ShellProfileID: processProfileID,
	})
	envelope := brokerRoundTrip(t, runtimeDir, protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: protocol.SessionCreateRequest,
		RequestID: requestID, Payload: payload,
	})
	if envelope.Type != protocol.SessionCreateResult || envelope.RequestID != requestID {
		t.Fatalf("create response = %#v", envelope)
	}
	result, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](envelope)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func sessionStatus(t *testing.T, runtimeDir, sessionID, generation string) protocol.BrokerRPCSessionRecord {
	t.Helper()
	payload, _ := json.Marshal(protocol.BrokerRPCEmptyPayload{})
	response := brokerRoundTrip(t, runtimeDir, protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: protocol.SessionStatusRequest, RequestID: processStatusRequest,
		SessionID: &sessionID, Generation: &generation, Payload: payload,
	})
	if response.Type != protocol.SessionStatusResult {
		t.Fatalf("status response = %#v", response)
	}
	result, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionResultPayload](response)
	if err != nil {
		t.Fatal(err)
	}
	return result.Session
}

func terminateSession(t *testing.T, runtimeDir, sessionID, generation string) protocol.BrokerRPCSessionMutationResultPayload {
	t.Helper()
	payload, _ := json.Marshal(protocol.BrokerRPCSessionTerminateRequestPayload{OperationID: processTerminateID})
	response := brokerRoundTrip(t, runtimeDir, protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: protocol.SessionTerminateRequest, RequestID: processTerminateReq,
		SessionID: &sessionID, Generation: &generation, Payload: payload,
	})
	if response.Type != protocol.SessionTerminateResult {
		t.Fatalf("terminate response = %#v", response)
	}
	result, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionMutationResultPayload](response)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func brokerRoundTrip(t *testing.T, runtimeDir string, request protocol.BrokerRPCEnvelope) protocol.BrokerRPCEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	connection, _, _, err := ipc.DialBroker(ctx, runtimeDir, protocol.Gateway, "process-integration")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.DecodeBrokerRPCRequest(data); err != nil {
		t.Fatal(err)
	}
	writer, _ := ipc.NewFrameWriter(connection, protocol.MaxInboundMessageBytes)
	reader, _ := ipc.NewFrameReader(connection, protocol.MaxOutboundMessageBytes)
	if err := writer.WriteFrame(data); err != nil {
		t.Fatal(err)
	}
	data, err = reader.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	response, err := protocol.DecodeBrokerRPCResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func waitBrokerReady(t *testing.T, ready <-chan host.BrokerReady) host.BrokerReady {
	t.Helper()
	select {
	case state := <-ready:
		return state
	case <-time.After(5 * time.Second):
		t.Fatal("broker did not become ready")
		return host.BrokerReady{}
	}
}

func waitBrokerHandshake(t *testing.T, runtimeDir string) protocol.IPCWelcomeClass {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_, welcome, err := ipc.ValidateBrokerEndpoint(ctx, runtimeDir, protocol.CLI, "process-integration")
		cancel()
		if err == nil {
			return welcome
		}
		if time.Now().After(deadline) {
			t.Fatalf("broker handshake did not become ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitGatewayReady(t *testing.T, ready <-chan gateway.ReadyState) gateway.ReadyState {
	t.Helper()
	select {
	case state := <-ready:
		return state
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not become ready")
		return gateway.ReadyState{}
	}
}

func waitForCounterFrame(t *testing.T, session broker.ManagedSession, afterRevision uint64) terminal.Frame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		frame, err := session.Snapshot(ctx)
		cancel()
		if err == nil && frame.Revision > afterRevision && strings.Contains(frameText(frame), "COUNTER pid=") {
			return frame
		}
		if time.Now().After(deadline) {
			t.Fatalf("counter frame did not advance beyond %d: revision=%d err=%v text=%q",
				afterRevision, frame.Revision, err, frameText(frame))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func frameText(frame terminal.Frame) string {
	var output strings.Builder
	for _, row := range frame.Screen {
		for _, cell := range row.Cells {
			if !cell.Continuation {
				output.WriteString(cell.Text)
			}
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func assertGatewayHealth(t *testing.T, address, generation string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("gateway health status = %d", response.StatusCode)
	}
	var payload struct {
		BrokerGeneration string `json:"brokerGeneration"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.BrokerGeneration != generation {
		t.Fatalf("health generation = %q, want %q", payload.BrokerGeneration, generation)
	}
}

func waitGatewayHealth(t *testing.T, address, generation string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		client := &http.Client{Timeout: 250 * time.Millisecond}
		response, err := client.Get("http://" + address + "/healthz")
		lastErr = err
		if err == nil {
			var payload struct {
				BrokerGeneration string `json:"brokerGeneration"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&payload)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && payload.BrokerGeneration == generation {
				return
			}
			lastErr = decodeErr
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway executable did not become healthy: %v", lastErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type runningCommand struct {
	command  *exec.Cmd
	done     chan error
	finished chan struct{}
	output   *lockedBuffer
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func startCommand(t *testing.T, executable string, arguments ...string) *runningCommand {
	t.Helper()
	output := &lockedBuffer{}
	command := exec.Command(executable, arguments...)
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	running := &runningCommand{
		command: command, done: make(chan error, 1), finished: make(chan struct{}), output: output,
	}
	go func() {
		running.done <- command.Wait()
		close(running.finished)
	}()
	t.Cleanup(func() {
		select {
		case <-running.finished:
		default:
			_ = running.command.Process.Kill()
			<-running.finished
		}
	})
	return running
}

func stopCommand(t *testing.T, running *runningCommand, name string) {
	t.Helper()
	if err := running.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal %s: %v", name, err)
	}
	select {
	case err := <-running.done:
		if err != nil {
			t.Fatalf("%s exit: %v\n%s", name, err, running.output.String())
		}
	case <-time.After(7 * time.Second):
		_ = running.command.Process.Kill()
		<-running.finished
		t.Fatalf("%s did not stop\n%s", name, running.output.String())
	}
}

func killCommand(t *testing.T, running *runningCommand, name string) {
	t.Helper()
	if err := running.command.Process.Kill(); err != nil {
		t.Fatalf("kill %s: %v", name, err)
	}
	select {
	case err := <-running.done:
		if err == nil {
			t.Fatalf("%s force-kill exited successfully", name)
		}
	case <-time.After(7 * time.Second):
		t.Fatalf("%s did not exit after force-kill\n%s", name, running.output.String())
	}
}

func reserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitProcessResult(t *testing.T, result <-chan error, name string) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("%s stopped with error: %v", name, err)
		}
	case <-time.After(7 * time.Second):
		t.Fatalf("%s did not stop", name)
	}
}
