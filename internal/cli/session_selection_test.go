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

func TestResolveSessionRefreshesExactGenerationWithoutMutation(t *testing.T) {
	listed := cliRecord(protocol.Running)
	current := cliRecord(protocol.Exited)
	management := &fakeManagement{
		pages:  []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{listed}}},
		status: current,
	}
	confirmer := &fakeConfirmer{}
	var output bytes.Buffer
	app := newTestApp(t, management, confirmer, &output, func() (string, error) {
		t.Fatal("resolution must not allocate a mutation ID")
		return "", nil
	})
	resolved, err := app.ResolveSession(context.Background(), cliSession)
	if err != nil || resolved.State != protocol.Exited || resolved.Generation != cliGeneration {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	if management.statusRef != (localclient.SessionRef{ID: cliSession, Generation: cliGeneration}) ||
		management.statusCalls != 1 || management.createCalls != 0 || management.terminateCalls != 0 || confirmer.calls != 0 || output.Len() != 0 {
		t.Fatalf("resolution had side effects or wrong identity: %+v", management)
	}
}

func TestResolveSessionRejectsMissingOrAmbiguousGenerations(t *testing.T) {
	first := cliRecord(protocol.Running)
	second := first
	second.Generation = cliOperation
	for _, test := range []struct {
		name    string
		records []protocol.BrokerRPCSessionRecord
		message string
	}{
		{"missing", nil, "managed session was not found"},
		{"ambiguous", []protocol.BrokerRPCSessionRecord{first, second}, "more than one generation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			management := &fakeManagement{pages: []protocol.BrokerRPCSessionListResultPayload{{Sessions: test.records}}}
			app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, NewOperationID)
			_, err := app.ResolveSession(context.Background(), cliSession)
			if err == nil || !strings.Contains(err.Error(), test.message) || management.statusCalls != 0 {
				t.Fatalf("status calls=%d err=%v", management.statusCalls, err)
			}
			if test.name == "missing" && !errors.Is(err, ErrSessionNotFound) {
				t.Fatal("missing-session error lost identity")
			}
		})
	}
}

func TestResolveSessionRejectsChangedStatusIdentity(t *testing.T) {
	for _, field := range []string{"session", "generation"} {
		t.Run(field, func(t *testing.T) {
			record := cliRecord(protocol.Running)
			changed := record
			if field == "session" {
				changed.ID = cliOperation
			} else {
				changed.Generation = cliOperation
			}
			management := &fakeManagement{pages: []protocol.BrokerRPCSessionListResultPayload{{Sessions: []protocol.BrokerRPCSessionRecord{record}}}, status: changed}
			app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, NewOperationID)
			_, err := app.ResolveSession(context.Background(), cliSession)
			if err == nil || !strings.Contains(err.Error(), "changed session identity") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreateUsesSavedConfigurationAndSingleOperationWithoutAttaching(t *testing.T) {
	for _, state := range []protocol.State{protocol.Running, protocol.Exited, protocol.Lost} {
		t.Run(string(state), func(t *testing.T) {
			record := cliRecord(state)
			management := &fakeManagement{create: protocol.BrokerRPCSessionMutationResultPayload{OperationID: cliOperation, Session: record}}
			confirmer := &fakeConfirmer{}
			var output bytes.Buffer
			ids := 0
			app := newTestApp(t, management, confirmer, &output, func() (string, error) { ids++; return cliOperation, nil })
			created, err := app.Create(context.Background(), cliProject, cliProfile)
			if err != nil || created.ID != record.ID || created.Generation != record.Generation || created.State != state {
				t.Fatalf("created=%+v err=%v", created, err)
			}
			if ids != 1 || management.createCalls != 1 || management.operationID != cliOperation || management.createdProject != cliProject || management.createdProfile != cliProfile {
				t.Fatalf("wrong creation arguments/calls: %+v IDs=%d", management, ids)
			}
			if management.listCalls != 0 || management.statusCalls != 0 || management.terminateCalls != 0 || confirmer.calls != 0 || output.Len() != 0 {
				t.Fatal("create performed unrelated side effects")
			}
		})
	}
}

func TestCreateRejectsInvalidIDsBeforeAllocatingOperation(t *testing.T) {
	for _, pair := range [][2]string{{"/tmp/project", cliProfile}, {cliProject, "zsh"}, {"", cliProfile}, {cliProject, strings.ToUpper(cliSession)}} {
		management := &fakeManagement{}
		app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, func() (string, error) { t.Fatal("invalid config allocated ID"); return "", nil })
		if _, err := app.Create(context.Background(), pair[0], pair[1]); err == nil || management.createCalls != 0 {
			t.Fatalf("invalid config accepted: %v", err)
		}
	}
}

func TestCreateCancellationAndIDFailuresNeverSend(t *testing.T) {
	for _, failure := range []string{"nil context", "pre-canceled", "ID failure", "invalid ID", "cancel during ID"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "nil context" {
				ctx = nil
			}
			if failure == "pre-canceled" {
				cancel()
			}
			management := &fakeManagement{}
			app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, func() (string, error) {
				switch failure {
				case "nil context", "pre-canceled":
					t.Fatal("invalid context allocated ID")
				case "ID failure":
					return "", errors.New("randomness unavailable")
				case "invalid ID":
					return "invalid", nil
				case "cancel during ID":
					cancel()
				}
				return cliOperation, nil
			})
			if _, err := app.Create(ctx, cliProject, cliProfile); err == nil || management.createCalls != 0 {
				t.Fatalf("failure allowed create: %v", err)
			}
		})
	}
}

func TestCreateUncertainOrRejectedDeliveryNeverRetries(t *testing.T) {
	denied := &localclient.RemoteError{Code: protocol.LimitExceeded, Message: "session limit reached"}
	for _, test := range []struct {
		name    string
		err     error
		message string
	}{
		{"uncertain", uncertainTestError{}, "delivery uncertain—run pterm list"},
		{"rejected", denied, "session limit reached"},
	} {
		t.Run(test.name, func(t *testing.T) {
			management := &fakeManagement{createErr: test.err}
			app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, func() (string, error) { return cliOperation, nil })
			_, err := app.Create(context.Background(), cliProject, cliProfile)
			if err == nil || !strings.Contains(err.Error(), test.message) || !errors.Is(err, test.err) || management.createCalls != 1 {
				t.Fatalf("calls=%d err=%v", management.createCalls, err)
			}
		})
	}
}

func TestCreateRejectsMismatchedAcknowledgementWithoutRetry(t *testing.T) {
	for _, field := range []string{"operation", "project", "profile", "session", "generation"} {
		t.Run(field, func(t *testing.T) {
			result := protocol.BrokerRPCSessionMutationResultPayload{OperationID: cliOperation, Session: cliRecord(protocol.Running)}
			switch field {
			case "operation":
				result.OperationID = cliSession
			case "project":
				result.Session.ProjectID = cliProfile
			case "profile":
				result.Session.ShellProfileID = cliProject
			case "session":
				result.Session.ID = "invalid"
			case "generation":
				result.Session.Generation = "invalid"
			}
			management := &fakeManagement{create: result}
			app := newTestApp(t, management, &fakeConfirmer{}, &bytes.Buffer{}, func() (string, error) { return cliOperation, nil })
			_, err := app.Create(context.Background(), cliProject, cliProfile)
			if err == nil || !strings.Contains(err.Error(), "delivery uncertain") || management.createCalls != 1 {
				t.Fatalf("calls=%d err=%v", management.createCalls, err)
			}
		})
	}
}
