package broker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/herdr/herdrtest"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestProviderPreflightRefusalsLeaveCreateQuota(t *testing.T) {
	for _, test := range []struct {
		name    string
		request protocol.HTTPProviderCreateRequest
		code    protocol.ErrorCode
	}{
		{"stale group", protocol.HTTPProviderCreateRequest{GroupID: "gffffffffffff", Name: "task"}, protocol.SessionMissing},
		{"missing kind", protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task", Agent: new("pi")}, protocol.InvalidRequest},
		{"not git", protocol.HTTPProviderCreateRequest{GroupID: "ws-w2", Name: "task"}, protocol.NotARepository},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newProviderFixture(t)
			fixture.waitState(t, protocol.HTTPProviderStateReady)
			for range providerCreatesPerMinute {
				response := fixture.create(t, fixture.credential, test.request)
				if code := gatewayErrorCode(t, response); code != test.code {
					t.Fatalf("refusal = %s, want %s", code, test.code)
				}
			}
			// A fake mutating RPC proves a valid attempt still reaches herdr.
			fixture.herdr.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
				return nil, &herdrtest.Error{Code: "worktree_create_failed", Message: "synthetic failure"}
			})
			for range providerCreatesPerMinute {
				response := fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task"})
				if code := gatewayErrorCode(t, response); code != protocol.WorktreeFailed {
					t.Fatalf("valid attempted create after preflight refusals = %s %q", code, gatewayErrorMessage(t, response))
				}
			}
			response := fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task"})
			if code := gatewayErrorCode(t, response); code != protocol.LimitExceeded {
				t.Fatalf("fourth mutating attempt = %s", code)
			}
		})
	}
}

func TestProviderUnavailableRefusalsLeaveCreateQuota(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	path := fixture.herdr.Path
	fixture.herdr.Stop()
	fixture.server.providers.provider.Poll()
	fixture.waitState(t, protocol.NotRunning)
	request := protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task"}
	for range providerCreatesPerMinute {
		if code := gatewayErrorCode(t, fixture.create(t, fixture.credential, request)); code != protocol.HostUnavailable {
			t.Fatalf("unavailable create = %s", code)
		}
	}
	server, err := herdrtest.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	snapshot, err := os.ReadFile(filepath.Join("..", "herdr", "testdata", "snapshot-0.8.2.json"))
	if err != nil {
		t.Fatal(err)
	}
	server.Snapshot(snapshot)
	server.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
		return nil, &herdrtest.Error{Code: "worktree_create_failed", Message: "synthetic failure"}
	})
	fixture.server.providers.provider.LookAgain()
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	if code := gatewayErrorCode(t, fixture.create(t, fixture.credential, request)); code != protocol.WorktreeFailed {
		t.Fatalf("create after server recovery = %s", code)
	}
}

func TestProviderAttemptedFailuresKeepCreateQuota(t *testing.T) {
	for _, manage := range []bool{false, true} {
		t.Run(map[bool]string{false: "not git after worktree RPC", true: "missing item after tab RPC"}[manage], func(t *testing.T) {
			fixture := newProviderFixture(t)
			fixture.waitState(t, protocol.HTTPProviderStateReady)
			method, code, want := "worktree.create", "not_git_worktree", protocol.NotARepository
			if manage {
				method, code, want = "tab.create", "workspace_not_found", protocol.SessionMissing
			}
			var attempted atomic.Int32
			fixture.herdr.Handle(method, func(json.RawMessage) (any, *herdrtest.Error) {
				attempted.Add(1)
				return nil, &herdrtest.Error{Code: code, Message: "synthetic refusal after RPC attempt"}
			})
			for range providerCreatesPerMinute {
				var response protocol.GatewayRPCEnvelope
				if manage {
					response = fixture.manage(t, fixture.credential, protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: new("w1"), Agent: new("claude")})
				} else {
					response = fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task"})
				}
				if got := gatewayErrorCode(t, response); got != want {
					t.Fatalf("attempt = %s, want %s", got, want)
				}
			}
			response := fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task"})
			if got := gatewayErrorCode(t, response); got != protocol.LimitExceeded || attempted.Load() != 3 {
				t.Fatalf("post-RPC error lost its charge: %s, attempts=%d", got, attempted.Load())
			}
		})
	}
}

func TestProviderReservationOneShotAndWindowIsolation(t *testing.T) {
	var limits providerLimits
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	first := limits.reserve("create", "phone", 3, now)
	second := limits.reserve("create", "phone", 3, now)
	third := limits.reserve("create", "phone", 3, now)
	if first == nil || second == nil || third == nil || limits.reserve("create", "phone", 3, now) != nil {
		t.Fatal("in-flight reservation bound")
	}
	second.finish(false)
	second.finish(false) // cannot return another request's unit
	replacement := limits.reserve("create", "phone", 3, now)
	if replacement == nil || limits.reserve("create", "phone", 3, now) != nil {
		t.Fatal("duplicate refund erased another charge")
	}
	third.finish(true)
	third.finish(false) // a committed attempt cannot be refunded later
	if limits.reserve("create", "phone", 3, now) != nil {
		t.Fatal("committed attempt refunded")
	}
	// Reset the clock, then return to exactly the old timestamp. Window
	// timestamps alone would confuse a delayed refund with the new window.
	if !limits.take("create", "phone", 3, now.Add(-time.Second)) {
		t.Fatal("clock reset did not replace window")
	}
	for range 2 {
		if !limits.take("create", "phone", 3, now) {
			t.Fatal("new window unexpectedly full")
		}
	}
	first.finish(false)
	replacement.finish(false)
	if limits.take("create", "phone", 3, now) {
		t.Fatal("old reservation refunded new window")
	}
	if !limits.take("create", "phone", 3, now.Add(time.Minute)) {
		t.Fatal("minute rollover did not reset")
	}
	if limits.take("create", "none", 0, now) || limits.take("create", "none", -1, now) {
		t.Fatal("zero/negative limit admitted")
	}
}

func TestProviderConcurrentReservationsStayBounded(t *testing.T) {
	var limits providerLimits
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	results := make(chan *providerReservation, 64)
	var workers sync.WaitGroup
	for range cap(results) {
		workers.Go(func() {
			if r := limits.reserve("create", "phone", 3, now); r != nil {
				results <- r
			}
		})
	}
	workers.Wait()
	close(results)
	var reservations []*providerReservation
	for reservation := range results {
		reservations = append(reservations, reservation)
	}
	if len(reservations) != 3 {
		t.Fatalf("concurrent admissions = %d", len(reservations))
	}
	for _, reservation := range reservations {
		for range 8 {
			workers.Go(func() { reservation.finish(false) })
		}
	}
	workers.Wait()
	for range 3 {
		if !limits.take("create", "phone", 3, now) {
			t.Fatal("one-shot concurrent refunds lost capacity")
		}
	}
	if limits.take("create", "phone", 3, now) {
		t.Fatal("refunds created capacity beyond limit")
	}
	if !limits.take("create", "other", 3, now) || !limits.take("manage", "phone", 30, now) {
		t.Fatal("another route/device lost independent capacity")
	}
}

func TestProviderAgentTabRefusalsLeaveCreateQuota(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.waitState(t, protocol.HTTPProviderStateReady)
	request := protocol.HTTPProviderManageRequest{Action: protocol.NewTab, ItemID: new("w77"), Agent: new("claude")}
	for range providerCreatesPerMinute {
		if code := gatewayErrorCode(t, fixture.manage(t, fixture.credential, request)); code != protocol.SessionMissing {
			t.Fatalf("stale agent tab = %s", code)
		}
	}
	fixture.herdr.Handle("worktree.create", func(json.RawMessage) (any, *herdrtest.Error) {
		return nil, &herdrtest.Error{Code: "worktree_create_failed", Message: "synthetic failure"}
	})
	if code := gatewayErrorCode(t, fixture.create(t, fixture.credential, protocol.HTTPProviderCreateRequest{GroupID: "g7d0f77ccdcfd", Name: "task"})); code != protocol.WorktreeFailed {
		t.Fatalf("create after refused agent tabs = %s", code)
	}
}
