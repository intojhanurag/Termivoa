//go:build darwin || linux

package broker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/transcript"
	"github.com/intojhanurag/phone-terminal/internal/transcript/transcripttest"
)

const chatAgentPID = 4242

var chatAgentStart = time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buf.String()
}

// chatFixture is a fake Claude Code in a temp folder, seen as a process of
// the stream fixture's session.
type chatFixture struct {
	agent *transcripttest.Session
	hub   *chatHub
	log   *lockedBuffer
	dead  atomic.Bool
}

func setUpChat(t *testing.T, server *ManagementServer, enabled bool) *chatFixture {
	t.Helper()
	base := t.TempDir()
	root, cwd := filepath.Join(base, ".claude"), filepath.Join(base, "app")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	agent, err := transcripttest.New(root, cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.Close() })
	if err := agent.PIDFile(chatAgentPID, chatAgentStart); err != nil {
		t.Fatal(err)
	}
	fixture := &chatFixture{agent: agent, hub: server.chat, log: &lockedBuffer{}}
	hub := server.chat
	hub.config = &transcript.Config{Claude: root, Dir: func(context.Context, int) (string, error) { return cwd, nil }}
	hub.processes = func(context.Context, ManagedSession) ([]transcript.Process, error) {
		if fixture.dead.Load() {
			return nil, nil
		}
		return []transcript.Process{{PID: chatAgentPID, Start: chatAgentStart}}, nil
	}
	hub.poll, hub.recheck, hub.linger, hub.log = 20*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond, fixture.log
	if enabled {
		if err := hub.setEnabled(context.Background(), true); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = hub.setEnabled(context.Background(), false) })
	return fixture
}

func chatState(t *testing.T, message protocol.Envelope) protocol.ChatStatePayload {
	t.Helper()
	state, err := protocol.DecodePayload[protocol.ChatStatePayload](message)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func chatPage(t *testing.T, message protocol.Envelope) protocol.ChatPagePayload {
	t.Helper()
	page, err := protocol.DecodePayload[protocol.ChatPagePayload](message)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestChatIsOffUntilTurnedOnAtTheComputer(t *testing.T) {
	fixture, _, _ := newStreamFixture(t)
	chat := setUpChat(t, fixture.server, false)
	chat.agent.Prompt("hello")
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	request := client.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10})
	if state := chatState(t, client.await(t, protocol.ChatState, request)); state.Link != protocol.ChatLinkOff {
		t.Fatalf("state = %+v", state)
	}
	if status := chat.hub.status(); status.Enabled || status.Tails != 0 {
		t.Fatalf("an off switch opens nothing: %+v", status)
	}
	// Only the admin channel turns it on.
	local := startRPCTestClient(t, fixture.server, protocol.CLI)
	result, _ := protocol.DecodeRPCPayload[protocol.AdminRPCChatStatusPayload](
		local.admin(t, protocol.AdminChatSet, protocol.AdminRPCChatSetPayload{Enabled: true}))
	if !result.Enabled || !result.Supported {
		t.Fatalf("status = %+v", result)
	}
}

func TestChatPullFlow(t *testing.T) {
	fixture, _, _ := newStreamFixture(t)
	chat := setUpChat(t, fixture.server, true)
	chat.agent.Turn("make the tests pass")
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.attach(t)
	request := client.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10})
	state := chatState(t, client.await(t, protocol.ChatState, request))
	if state.Link != protocol.ChatLinkLinked || state.HistoryID == nil || *state.Agent != protocol.Claude {
		t.Fatalf("state = %+v", state)
	}
	page := chatPage(t, client.await(t, protocol.ChatPage, request))
	if len(page.Blocks) != 5 || page.HistoryID != *state.HistoryID || !page.Complete {
		t.Fatalf("page = %+v", page)
	}
	if log := chat.log.String(); !strings.Contains(log, "chat open device=") || !strings.Contains(log, "session="+rpcSessionID) ||
		strings.Contains(log, chat.agent.ID) || strings.Contains(log, chat.agent.Path) {
		t.Fatalf("audit line = %q", log)
	}

	chat.agent.Prompt("and now the docs")
	changed, _ := protocol.DecodePayload[protocol.ChatChangedPayload](client.await(t, protocol.ChatChanged, ""))
	if changed.HistoryID != page.HistoryID {
		t.Fatalf("changed = %+v", changed)
	}
	request = client.action(t, protocol.ChatRead, protocol.ChatReadPayload{After: &page.After, Limit: 10})
	next := chatPage(t, client.await(t, protocol.ChatPage, request))
	if len(next.Blocks) != 1 || *next.Blocks[0].Text != "and now the docs" {
		t.Fatalf("next = %+v", next.Blocks)
	}

	// A cursor this host did not issue is refused; the stream stays open.
	bad := strings.Repeat("A", 22)
	request = client.action(t, protocol.ChatRead, protocol.ChatReadPayload{Before: &bad, Limit: 10})
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.HistoryChanged {
		t.Fatalf("bad cursor = %+v", failure)
	}

	// One chat request per session: a running one refuses the next.
	chat.hub.mu.Lock()
	tail := chat.hub.tails[rpcSessionID]
	chat.hub.mu.Unlock()
	tail.busy.Store(true)
	request = client.action(t, protocol.ChatRead, protocol.ChatReadPayload{After: &next.After, Limit: 10})
	failure, _ = protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.LimitExceeded {
		t.Fatalf("busy = %+v", failure)
	}
	tail.busy.Store(false)

	// The agent exits: ended, and the last page stays readable.
	chat.dead.Store(true)
	ended := chatState(t, client.await(t, protocol.ChatState, ""))
	if ended.Link != protocol.ChatLinkEnded || ended.HistoryID == nil {
		t.Fatalf("ended = %+v", ended)
	}
	request = client.action(t, protocol.ChatRead, protocol.ChatReadPayload{After: &page.After, Limit: 10})
	chatPage(t, client.await(t, protocol.ChatPage, request))
}

func TestChatRefusesThumbnailsAndReadsBeforeOpen(t *testing.T) {
	fixture, _, _ := newStreamFixture(t)
	setUpChat(t, fixture.server, true)
	client := startStreamClient(t, fixture.server, protocol.CLI)
	client.await(t, protocol.Welcome, "")
	client.action(t, protocol.SessionAttach, protocol.SessionAttachPayload{Thumbnail: &protocol.ThumbnailMode{MaxUpdatesPerSecond: 1}})
	client.await(t, protocol.SnapshotEnd, "")
	request := client.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10})
	failure, _ := protocol.DecodePayload[protocol.ErrorPayload](client.await(t, protocol.Error, request))
	if failure.Code != protocol.NotController {
		t.Fatalf("thumbnail = %+v", failure)
	}
	other := startStreamClient(t, fixture.server, protocol.CLI)
	other.attach(t)
	cursor := strings.Repeat("A", 22)
	request = other.action(t, protocol.ChatRead, protocol.ChatReadPayload{After: &cursor, Limit: 1})
	failure, _ = protocol.DecodePayload[protocol.ErrorPayload](other.await(t, protocol.Error, request))
	if failure.Code != protocol.InvalidRequest {
		t.Fatalf("read before open = %+v", failure)
	}
}

func TestChatOffFromAnyDeviceClosesEveryChat(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	chat := setUpChat(t, fixture.server, true)
	chat.agent.Prompt("hi")
	phone := pairDevice(t, service, "Phone")
	client := startBrowserFrom(t, fixture.server, phone.Credential, "")
	client.attach(t)
	request := client.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10})
	client.await(t, protocol.ChatPage, request)

	if _, err := protocol.DecodeGatewayRPCRequest([]byte(`{"version":1,"type":"gateway.chat.update","requestId":"` +
		rpcSessionGen + `","payload":{"credential":"` + phone.Credential + `","request":{"enabled":true}}}`)); err == nil {
		t.Fatal("the protocol lets a device turn Chat on")
	}
	rpc := startRPCTestClient(t, fixture.server, protocol.Gateway)
	off := rpc.gateway(t, protocol.GatewayChatUpdate, protocol.GatewayRPCChatUpdatePayload{
		Credential: phone.Credential, Request: protocol.HTTPChatUpdateRequest{Enabled: false}})
	if info, err := protocol.DecodeRPCPayload[protocol.HTTPChatInfo](off); err != nil || info.Enabled {
		t.Fatalf("off = %s", off.Payload)
	}
	if state := chatState(t, client.await(t, protocol.ChatState, "")); state.Link != protocol.ChatLinkOff {
		t.Fatalf("state = %+v", state)
	}
	if status := chat.hub.status(); status.Enabled || status.Tails != 0 {
		t.Fatalf("status = %+v", status)
	}
	settings, _ := fixture.server.browserSettings(context.Background(), principalOf(t, fixture.server, phone.Credential))
	if settings.Chat == nil || settings.Chat.Enabled {
		t.Fatalf("settings chat = %+v", settings.Chat)
	}
}

func principalOf(t *testing.T, server *ManagementServer, credential string) auth.Principal {
	t.Helper()
	principal, err := server.config.Auth.Authenticate(context.Background(), credential)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func TestChatRevocationRemovesTheSubscriberAtOnce(t *testing.T) {
	fixture, _, _, service, _ := newAuthStreamFixture(t)
	chat := setUpChat(t, fixture.server, true)
	chat.agent.Prompt("hi")
	phone := pairDevice(t, service, "Phone")
	client := startBrowserFrom(t, fixture.server, phone.Credential, "")
	client.attach(t)
	request := client.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10})
	client.await(t, protocol.ChatPage, request)
	if _, err := service.RevokeDevice(context.Background(), phone.DeviceID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		chat.hub.mu.Lock()
		tail := chat.hub.tails[rpcSessionID]
		chat.hub.mu.Unlock()
		if tail == nil {
			return true
		}
		tail.mu.Lock()
		defer tail.mu.Unlock()
		return len(tail.subs) == 0
	})
	chat.agent.Prompt("after revoke")
	client.await(t, protocol.AccessRevoked, "")
	for {
		select {
		case message := <-client.messages:
			if message.Type == protocol.ChatChanged || message.Type == protocol.ChatPage {
				t.Fatalf("sent after revocation: %s", message.Type)
			}
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

func TestChatTailsAreSharedCappedAndLinger(t *testing.T) {
	fixture, session, _ := newStreamFixture(t)
	chat := setUpChat(t, fixture.server, true)
	chat.agent.Prompt("hi")
	first := startStreamClient(t, fixture.server, protocol.CLI)
	first.attach(t)
	second := startStreamClient(t, fixture.server, protocol.CLI)
	second.attach(t)
	first.await(t, protocol.ChatPage, first.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10}))
	second.await(t, protocol.ChatPage, second.action(t, protocol.ChatOpen, protocol.ChatOpenPayload{Limit: 10}))
	if status := chat.hub.status(); status.Tails != 1 {
		t.Fatalf("one tail per session: %+v", status)
	}
	for index := range maxChatTails - 1 {
		if _, ok := chat.hub.tailFor(fmt.Sprintf("other-%d", index), session); !ok {
			t.Fatal("room for 8 tails")
		}
	}
	if _, ok := chat.hub.tailFor("ninth", session); ok {
		t.Fatal("a ninth tail started")
	}
	first.action(t, protocol.ChatClose, protocol.EmptyPayload{})
	_ = second.conn.Close() // a detach closes its chat too
	waitFor(t, func() bool {
		chat.hub.mu.Lock()
		defer chat.hub.mu.Unlock()
		_, ok := chat.hub.tails[rpcSessionID]
		return !ok
	})
}
