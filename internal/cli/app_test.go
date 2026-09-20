package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	cliSession    = "a0000000-0000-4000-8000-000000000001"
	cliGeneration = "10000000-0000-4000-8000-000000000002"
	cliProject    = "10000000-0000-4000-8000-000000000003"
	cliProfile    = "10000000-0000-4000-8000-000000000004"
	cliOperation  = "10000000-0000-4000-8000-000000000005"
)

type fakeManagement struct {
	pages          []protocol.BrokerRPCSessionListResultPayload
	listCalls      int
	status         protocol.BrokerRPCSessionRecord
	statusErr      error
	terminate      protocol.BrokerRPCSessionMutationResultPayload
	terminateErr   error
	terminateCalls int
	terminatedRef  localclient.SessionRef
	operationID    string
}

func (management *fakeManagement) ListPage(
	context.Context,
	*string,
	int64,
) (protocol.BrokerRPCSessionListResultPayload, error) {
	if management.listCalls >= len(management.pages) {
		return protocol.BrokerRPCSessionListResultPayload{}, errors.New("unexpected list page")
	}
	page := management.pages[management.listCalls]
	management.listCalls++
	return page, nil
}

func (management *fakeManagement) Status(
	_ context.Context,
	_ localclient.SessionRef,
) (protocol.BrokerRPCSessionRecord, error) {
	return management.status, management.statusErr
}

func (management *fakeManagement) Terminate(
	_ context.Context,
	ref localclient.SessionRef,
	operationID string,
) (protocol.BrokerRPCSessionMutationResultPayload, error) {
	management.terminateCalls++
	management.terminatedRef = ref
	management.operationID = operationID
	return management.terminate, management.terminateErr
}

type fakeConfirmer struct {
	answer bool
	err    error
	calls  int
	prompt string
}

func (confirmer *fakeConfirmer) Confirm(_ context.Context, prompt string) (bool, error) {
	confirmer.calls++
	confirmer.prompt = prompt
	return confirmer.answer, confirmer.err
}

func TestListEscapesLegacyTerminalControls(t *testing.T) {
	record := cliRecord(protocol.Running)
	record.Title = "legacy\x1b]52;c;payload\a\nright-to-left\u202e"
	management := &fakeManagement{pages: []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}}}
	var output bytes.Buffer
	app := newTestApp(t, management, &fakeConfirmer{}, &output, func() (string, error) { return cliOperation, nil })
	if err := app.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.ContainsAny(text, "\x1b\a\u202e") {
		t.Fatalf("output contains raw terminal or bidi controls: %q", text)
	}
	for _, escaped := range []string{`\x1b`, `\a`, `\n`, `\u202e`} {
		if !strings.Contains(text, escaped) {
			t.Fatalf("output does not visibly escape %q: %q", escaped, text)
		}
	}
	if strings.Count(text, "\n") != 2 {
		t.Fatalf("stored title changed output row count: %q", text)
	}
}

func TestListRejectsDuplicateIdentityAcrossPages(t *testing.T) {
	record := cliRecord(protocol.Running)
	next := "next"
	management := &fakeManagement{pages: []protocol.BrokerRPCSessionListResultPayload{
		{Sessions: []protocol.BrokerRPCSessionRecord{record}, NextCursor: &next},
		{Sessions: []protocol.BrokerRPCSessionRecord{record}},
	}}
	app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, func() (string, error) { return cliOperation, nil })
	err := app.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "duplicate identity") {
		t.Fatalf("expected duplicate pagination failure, got %v", err)
	}
}

func TestListRejectsRepeatedCursor(t *testing.T) {
	next := "same"
	management := &fakeManagement{pages: []protocol.BrokerRPCSessionListResultPayload{
		{NextCursor: &next}, {NextCursor: &next},
	}}
	app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, func() (string, error) { return cliOperation, nil })
	err := app.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "repeated a cursor") {
		t.Fatalf("expected cursor loop failure, got %v", err)
	}
}

func TestTerminateUsesExactIdentityOneOperationAndSafePrompt(t *testing.T) {
	record := cliRecord(protocol.Running)
	record.Title = "work\x1b[31m\nunsafe"
	accepted := record
	accepted.State = protocol.Exiting
	management := &fakeManagement{
		pages:     []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}},
		status:    record,
		terminate: protocol.BrokerRPCSessionMutationResultPayload{OperationID: cliOperation, Session: accepted},
	}
	confirmer := &fakeConfirmer{answer: true}
	var output bytes.Buffer
	idCalls := 0
	app := newTestApp(t, management, confirmer, &output, func() (string, error) {
		idCalls++
		return cliOperation, nil
	})
	if err := app.Terminate(context.Background(), cliSession); err != nil {
		t.Fatal(err)
	}
	if confirmer.calls != 1 || strings.ContainsAny(confirmer.prompt, "\x1b\n") ||
		!strings.Contains(confirmer.prompt, `\x1b`) || !strings.Contains(confirmer.prompt, `\n`) {
		t.Fatalf("unsafe or missing confirmation prompt: %q", confirmer.prompt)
	}
	if idCalls != 1 || management.terminateCalls != 1 || management.operationID != cliOperation {
		t.Fatalf("IDs/calls: id=%d terminate=%d operation=%q", idCalls, management.terminateCalls, management.operationID)
	}
	if management.terminatedRef != (localclient.SessionRef{ID: cliSession, Generation: cliGeneration}) {
		t.Fatalf("wrong terminated identity: %+v", management.terminatedRef)
	}
	if !strings.Contains(output.String(), "Termination accepted") || !strings.Contains(output.String(), "is exiting") {
		t.Fatalf("unexpected acknowledgement: %q", output.String())
	}
}

func TestTerminateDeclineDoesNotAllocateOperationOrSend(t *testing.T) {
	record := cliRecord(protocol.Running)
	management := &fakeManagement{
		pages:  []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}},
		status: record,
	}
	confirmer := &fakeConfirmer{answer: false}
	var output bytes.Buffer
	idCalls := 0
	app := newTestApp(t, management, confirmer, &output, func() (string, error) {
		idCalls++
		return cliOperation, nil
	})
	if err := app.Terminate(context.Background(), cliSession); err != nil {
		t.Fatal(err)
	}
	if idCalls != 0 || management.terminateCalls != 0 || output.String() != "Termination cancelled.\n" {
		t.Fatalf("decline caused side effects: IDs=%d calls=%d output=%q", idCalls, management.terminateCalls, output.String())
	}
}

func TestTerminateDoesNotTreatTerminalOrExitingSessionAsLive(t *testing.T) {
	for _, state := range []protocol.State{protocol.Exiting, protocol.Exited, protocol.Lost} {
		t.Run(string(state), func(t *testing.T) {
			record := cliRecord(state)
			management := &fakeManagement{
				pages:  []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}},
				status: record,
			}
			confirmer := &fakeConfirmer{answer: true}
			var output bytes.Buffer
			app := newTestApp(t, management, confirmer, &output, func() (string, error) {
				t.Fatal("operation ID must not be generated")
				return "", nil
			})
			if err := app.Terminate(context.Background(), cliSession); err != nil {
				t.Fatal(err)
			}
			if confirmer.calls != 0 || management.terminateCalls != 0 {
				t.Fatalf("state %s was treated as live", state)
			}
			if state == protocol.Exiting && !strings.Contains(output.String(), "already exiting") {
				t.Fatalf("unexpected exiting output: %q", output.String())
			}
			if (state == protocol.Exited || state == protocol.Lost) && !strings.Contains(output.String(), "no live process") {
				t.Fatalf("terminal state implied a live process: %q", output.String())
			}
		})
	}
}

func TestTerminateUncertainDeliveryRequiresInspectionWithoutRetry(t *testing.T) {
	record := cliRecord(protocol.Running)
	management := &fakeManagement{
		pages:        []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}},
		status:       record,
		terminateErr: uncertainTestError{},
	}
	app := newTestApp(t, management, &fakeConfirmer{answer: true}, &bytes.Buffer{},
		func() (string, error) { return cliOperation, nil })
	err := app.Terminate(context.Background(), cliSession)
	if err == nil || !strings.Contains(err.Error(), "delivery uncertain—run pterm list") {
		t.Fatalf("unexpected error: %v", err)
	}
	if management.terminateCalls != 1 {
		t.Fatalf("terminate retried %d times", management.terminateCalls)
	}
}

func TestTerminateRequiresCanonicalExactSessionID(t *testing.T) {
	record := cliRecord(protocol.Running)
	for _, sessionID := range []string{cliSession[:8], strings.ToUpper(cliSession)} {
		t.Run(sessionID, func(t *testing.T) {
			management := &fakeManagement{
				pages:  []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}},
				status: record,
			}
			app := newTestApp(t, management, &fakeConfirmer{answer: true}, &bytes.Buffer{},
				func() (string, error) { return cliOperation, nil })
			err := app.Terminate(context.Background(), sessionID)
			if err == nil || !strings.Contains(err.Error(), "canonical lowercase UUID") {
				t.Fatalf("expected canonical UUID rejection, got %v", err)
			}
			if management.listCalls != 0 || management.terminateCalls != 0 {
				t.Fatal("invalid ID reached management client")
			}
		})
	}
}

func TestLineConfirmerBoundsAndParsesInput(t *testing.T) {
	for _, test := range []struct {
		name   string
		input  string
		answer bool
		err    bool
	}{
		{"yes", "YES\n", true, false},
		{"default no", "\n", false, false},
		{"explicit no", "no\n", false, false},
		{"invalid", "maybe\n", false, true},
		{"bounded", strings.Repeat("y", confirmationBufferBytes*2) + "\n", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			confirmer, err := NewLineConfirmer(strings.NewReader(test.input), &output)
			if err != nil {
				t.Fatal(err)
			}
			answer, err := confirmer.Confirm(context.Background(), "Safe prompt [y/N] ")
			if (err != nil) != test.err || answer != test.answer {
				t.Fatalf("answer=%v err=%v", answer, err)
			}
			if output.String() != "Safe prompt [y/N] " {
				t.Fatalf("unexpected prompt output %q", output.String())
			}
		})
	}
}

type uncertainTestError struct{}

func (uncertainTestError) Error() string { return "lost response" }

func (uncertainTestError) As(target any) bool {
	pointer, ok := target.(**localclient.DeliveryUncertainError)
	if !ok {
		return false
	}
	*pointer = nil
	return true
}

func cliRecord(state protocol.State) protocol.BrokerRPCSessionRecord {
	record := protocol.BrokerRPCSessionRecord{
		ID: cliSession, Generation: cliGeneration, Title: "Terminal",
		ProjectID: cliProject, ShellProfileID: cliProfile, State: state,
		Columns: 80, Rows: 24, CreatedAt: "2026-09-21T00:00:00Z", UpdatedAt: "2026-09-21T00:00:01Z",
	}
	if state == protocol.Exited || state == protocol.Lost {
		ended := "2026-09-21T00:00:01Z"
		record.EndedAt = &ended
	}
	return record
}

func newTestApp(
	t *testing.T,
	management Management,
	confirmer Confirmer,
	output *bytes.Buffer,
	newID IDSource,
) *App {
	t.Helper()
	app, err := NewApp(management, confirmer, newID, output)
	if err != nil {
		t.Fatal(err)
	}
	return app
}
