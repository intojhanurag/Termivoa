package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *testClock) Set(value time.Time) {
	clock.mu.Lock()
	clock.now = value
	clock.mu.Unlock()
}

var serviceStart = time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)

func newTestService(t *testing.T) (*Service, *testClock) {
	t.Helper()
	persistence, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "auth.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	clock := &testClock{now: serviceStart}
	service, err := NewService(ServiceConfig{Store: persistence, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return service, clock
}

// pair runs the complete local-grant/phone-redeem/local-approve/phone-poll flow.
func pair(t *testing.T, service *Service, label string) PollResult {
	t.Helper()
	ctx := context.Background()
	grant, _, err := service.CreateGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := service.Redeem(ctx, grant, label)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Approve(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	result, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret)
	if err != nil || !result.Approved {
		t.Fatalf("approved poll = %#v, %v", result, err)
	}
	return result
}

func TestPairingFlowIssuesCredentialExactlyOnce(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	grant, expires, err := service.CreateGrant(ctx)
	if err != nil || !expires.Equal(serviceStart.Add(PairingGrantLifetime)) || protocol.ValidateSecretText(grant) != nil {
		t.Fatalf("grant = %v, %v", expires, err)
	}
	redeemed, err := service.Redeem(ctx, grant, "Phone")
	if err != nil || protocol.ValidateVerificationCode(redeemed.VerificationCode) != nil || redeemed.PollSecret == grant {
		t.Fatalf("redeem = %#v, %v", redeemed, err)
	}
	if _, err := service.Redeem(ctx, grant, "Second phone"); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("second redemption = %v", err)
	}
	if result, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret); err != nil || result.Approved || result.Credential != "" {
		t.Fatalf("pending poll = %#v, %v", result, err)
	}
	pending, err := service.Pending(ctx)
	if err != nil || len(pending) != 1 || pending[0].VerificationCode != redeemed.VerificationCode {
		t.Fatalf("local pending view = %#v, %v", pending, err)
	}
	wrong, _ := NewSecret()
	wrongValue, _ := wrong.TransportValue()
	if _, err := service.Poll(ctx, redeemed.PendingID, wrongValue); !errors.Is(err, ErrPollSecret) {
		t.Fatalf("wrong poll secret = %v", err)
	}
	if _, err := service.Poll(ctx, redeemed.PendingID, redeemed.VerificationCode); !errors.Is(err, ErrPollSecret) {
		t.Fatalf("verification code accepted as poll secret: %v", err)
	}
	if err := service.Approve(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	if err := service.Approve(ctx, redeemed.PendingID); !errors.Is(err, ErrApproved) {
		t.Fatalf("approval replay = %v", err)
	}
	result, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret)
	if err != nil || !result.Approved || result.DeviceLabel != "Phone" || result.Credential == "" ||
		!result.ExpiresAt.Equal(serviceStart.Add(BrowserAbsoluteLifetime)) {
		t.Fatalf("approved poll = %#v, %v", result, err)
	}
	// A lost issuance response cannot be replayed into a second credential.
	if _, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("issuance replay = %v", err)
	}
	principal, err := service.Authenticate(ctx, result.Credential)
	if err != nil || principal.DeviceID != result.DeviceID || principal.DeviceLabel != "Phone" {
		t.Fatalf("authenticate = %#v, %v", principal, err)
	}
	for _, credential := range []string{"", grant, redeemed.PollSecret, strings.Repeat("A", 43), result.Credential + "A"} {
		if _, err := service.Authenticate(ctx, credential); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("non-credential authenticated: %v", err)
		}
	}
}

func TestConcurrentRedemptionHasOneWinner(t *testing.T) {
	service, _ := newTestService(t)
	grant, _, err := service.CreateGrant(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.Redeem(context.Background(), grant, "Phone")
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			} else if !errors.Is(err, ErrPairingExpired) {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if wins != 1 {
		t.Fatalf("winners = %d", wins)
	}
}

func TestGrantExpiryRateLimitAndLabelBounds(t *testing.T) {
	service, clock := newTestService(t)
	ctx := context.Background()
	grant, _, err := service.CreateGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, grant, "bad\x1blabel"); !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("control label = %v", err)
	}
	clock.Set(serviceStart.Add(PairingGrantLifetime))
	if _, err := service.Redeem(ctx, grant, "Phone"); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired grant = %v", err)
	}
	for range RedeemAttemptsPerMin - 1 {
		if _, err := service.Redeem(ctx, grant, "Phone"); !errors.Is(err, ErrPairingExpired) {
			t.Fatalf("attempt below the limit = %v", err)
		}
	}
	if _, err := service.Redeem(ctx, grant, "Phone"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("attempt above the limit = %v", err)
	}
	clock.Set(serviceStart.Add(PairingGrantLifetime + time.Minute))
	if _, err := service.Redeem(ctx, grant, "Phone"); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("attempt after the window = %v", err)
	}
}

func TestCredentialLifetimeExpiryAndClockRollback(t *testing.T) {
	service, clock := newTestService(t)
	ctx := context.Background()
	issued := pair(t, service, "Phone")
	for day := 6; day < 30; day += 6 {
		clock.Set(serviceStart.Add(time.Duration(day) * 24 * time.Hour))
		if _, err := service.Authenticate(ctx, issued.Credential); err != nil {
			t.Fatalf("regular use on day %d rejected: %v", day, err)
		}
	}
	clock.Set(serviceStart.Add(BrowserAbsoluteLifetime - time.Nanosecond))
	if _, err := service.Authenticate(ctx, issued.Credential); err != nil {
		t.Fatalf("before absolute expiry: %v", err)
	}
	clock.Set(serviceStart.Add(BrowserAbsoluteLifetime))
	if _, err := service.Authenticate(ctx, issued.Credential); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("absolute expiry = %v", err)
	}

	clock.Set(serviceStart)
	idle := pair(t, service, "Idle")
	clock.Set(serviceStart.Add(BrowserInactivityLifetime))
	if _, err := service.Authenticate(ctx, idle.Credential); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("inactivity expiry = %v", err)
	}
	clock.Set(serviceStart.Add(-time.Hour))
	fresh := pair(t, service, "Fresh")
	clock.Set(serviceStart.Add(-2 * time.Hour))
	if _, err := service.Authenticate(ctx, fresh.Credential); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("clock rollback before creation = %v", err)
	}
}

func TestCrossProfileCredentialIsRejected(t *testing.T) {
	production, _ := newTestService(t)
	development, _ := newTestService(t)
	issued := pair(t, development, "Dev phone")
	if _, err := production.Authenticate(context.Background(), issued.Credential); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("development credential accepted by production profile: %v", err)
	}
}

func TestRevokeDeviceClosesBindingsBeforeReportingSuccess(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	issued := pair(t, service, "Phone")
	other := pair(t, service, "Tablet")
	binding, principal, err := service.Bind(ctx, issued.Credential)
	if err != nil || principal.DeviceID != issued.DeviceID || binding.Check() != nil {
		t.Fatalf("bind = %#v, %v", principal, err)
	}
	otherBinding, _, err := service.Bind(ctx, other.Credential)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		<-binding.Revoked()
		if !errors.Is(binding.Check(), ErrRevoked) {
			t.Error("revoked binding still authorizes actions")
		}
		binding.Release()
		close(released)
	}()
	closed, err := service.RevokeDevice(ctx, issued.DeviceID)
	if err != nil || closed != 1 {
		t.Fatalf("revoke = %d, %v", closed, err)
	}
	select {
	case <-released:
	default:
		t.Fatal("revocation reported success before the stream released")
	}
	if _, err := service.Authenticate(ctx, issued.Credential); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked credential = %v", err)
	}
	if _, _, err := service.Bind(ctx, issued.Credential); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked credential bound: %v", err)
	}
	if otherBinding.Check() != nil {
		t.Fatal("revocation affected an unrelated device")
	}
	if _, err := service.RevokeDevice(ctx, "00000000-0000-4000-8000-000000000999"); !errors.Is(err, ErrDeviceMissing) {
		t.Fatalf("unknown device revoke = %v", err)
	}
	// A stream that never releases makes revocation report incomplete cleanup.
	service.cleanupTimeout = 20 * time.Millisecond
	if _, err := service.RevokeDevice(ctx, other.DeviceID); !errors.Is(err, ErrCleanup) {
		t.Fatalf("stuck stream cleanup = %v", err)
	}
	otherBinding.Release()
}

func TestLogoutRevokesOnlyThatCredential(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	first, second := pair(t, service, "Phone"), pair(t, service, "Laptop")
	binding, _, err := service.Bind(ctx, first.Credential)
	if err != nil {
		t.Fatal(err)
	}
	go func() { <-binding.Revoked(); binding.Release() }()
	if err := service.Logout(ctx, first.Credential); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, first.Credential); !errors.Is(err, ErrRevoked) {
		t.Fatalf("logged-out credential = %v", err)
	}
	if _, err := service.Authenticate(ctx, second.Credential); err != nil {
		t.Fatalf("logout affected another device: %v", err)
	}
}

func TestRevocationFailureStaysFailClosed(t *testing.T) {
	service, _ := newTestService(t)
	issued := pair(t, service, "Phone")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.RevokeDevice(canceled, issued.DeviceID); err == nil || errors.Is(err, ErrDeviceMissing) {
		t.Fatalf("failed durable revocation reported %v", err)
	}
	if _, err := service.Authenticate(context.Background(), issued.Credential); !errors.Is(err, ErrRevoked) {
		t.Fatalf("in-memory revocation did not fail closed: %v", err)
	}
}

// TestDevicesReportsPendingRevokedAfterFailedPersistence covers a revocation
// whose SQLite write failed: the store row still looks active, but the
// service keeps the revocation fail-closed in memory. Devices() must report
// that device as revoked (and exclude it from the cap; see the store-level
// TestDeviceCapacityExcludesPendingRevoked), not surface the stale SQLite
// status.
func TestDevicesReportsPendingRevokedAfterFailedPersistence(t *testing.T) {
	service, _ := newTestService(t)
	issued := pair(t, service, "Phone")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.RevokeDevice(canceled, issued.DeviceID); err == nil || errors.Is(err, ErrDeviceMissing) {
		t.Fatalf("failed durable revocation reported %v", err)
	}
	devices, err := service.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var status store.DeviceStatus
	found := false
	for _, device := range devices {
		if device.ID == issued.DeviceID {
			status, found = device.Status, true
		}
	}
	if !found {
		t.Fatalf("device %s missing from Devices()", issued.DeviceID)
	}
	if status != store.DeviceRevoked {
		t.Fatalf("device status = %q, want revoked (in-memory revocation not consulted)", status)
	}
}

func TestRevocationRacingBindNeverLeavesAnAuthorizedStream(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	for range 20 {
		issued := pair(t, service, "Phone")
		bindings := make(chan *Binding, 1)
		go func() {
			binding, _, err := service.Bind(ctx, issued.Credential)
			if err != nil {
				bindings <- nil
				return
			}
			go func() { <-binding.Revoked(); binding.Release() }()
			bindings <- binding
		}()
		if _, err := service.RevokeDevice(ctx, issued.DeviceID); err != nil {
			t.Fatal(err)
		}
		if binding := <-bindings; binding != nil && binding.Check() == nil {
			t.Fatal("a binding survived a completed revocation")
		}
	}
}

func TestBindingExpiresOnTheLiveConnection(t *testing.T) {
	service, clock := newTestService(t)
	issued := pair(t, service, "Phone")
	for day := 6; day < 30; day += 6 {
		clock.Set(serviceStart.Add(time.Duration(day) * 24 * time.Hour))
		if _, err := service.Authenticate(context.Background(), issued.Credential); err != nil {
			t.Fatal(err)
		}
	}
	clock.Set(serviceStart.Add(BrowserAbsoluteLifetime - 50*time.Millisecond))
	binding, _, err := service.Bind(context.Background(), issued.Credential)
	if err != nil || binding.Check() != nil {
		t.Fatalf("bind before expiry: %v", err)
	}
	defer binding.Release()
	select {
	case <-binding.Revoked():
	case <-time.After(5 * time.Second):
		t.Fatal("open binding outlived absolute expiry")
	}
	if !errors.Is(binding.Check(), ErrExpired) {
		t.Fatalf("expired binding check = %v", binding.Check())
	}
}

func TestVerificationCodeFormat(t *testing.T) {
	for range 100 {
		code, err := verificationCode(randReader{})
		if err != nil || protocol.ValidateVerificationCode(code) != nil {
			t.Fatalf("code = %q, %v", code, err)
		}
	}
	if _, err := verificationCode(strings.NewReader("")); !errors.Is(err, ErrRandomness) {
		t.Fatal("empty random source produced a code")
	}
}

type randReader struct{}

func (randReader) Read(data []byte) (int, error) {
	secret, err := NewSecret()
	if err != nil {
		return 0, err
	}
	return copy(data, secret.value[:]), nil
}

// A wall clock moved behind a grant or pending pairing's creation must not
// extend its two- or five-minute lifetime; the rejected attempts consume
// nothing, so pairing resumes once the clock is back.
func TestPairingRejectsClockRollback(t *testing.T) {
	service, clock := newTestService(t)
	ctx := context.Background()
	grant, _, err := service.CreateGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(serviceStart.Add(-time.Hour))
	if _, err := service.Redeem(ctx, grant, "Phone"); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("grant redeemed behind its creation time: %v", err)
	}
	clock.Set(serviceStart.Add(time.Second))
	redeemed, err := service.Redeem(ctx, grant, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(serviceStart.Add(-time.Hour))
	if err := service.Approve(ctx, redeemed.PendingID); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("pending approved behind its creation time: %v", err)
	}
	if pending, err := service.Pending(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending listed behind its creation time: %d, %v", len(pending), err)
	}
	clock.Set(serviceStart.Add(2 * time.Second))
	if err := service.Approve(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	clock.Set(serviceStart.Add(-time.Hour))
	if result, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret); !errors.Is(err, ErrPairingExpired) ||
		result.Credential != "" {
		t.Fatalf("credential issued behind the pending creation time: %#v, %v", result, err)
	}
	clock.Set(serviceStart.Add(3 * time.Second))
	if result, err := service.Poll(ctx, redeemed.PendingID, redeemed.PollSecret); err != nil || !result.Approved {
		t.Fatalf("poll after the clock recovered = %#v, %v", result, err)
	}
}

// Go timers use a monotonic clock that pauses while the host sleeps. A live
// binding must still end once the wall clock passes its validity, instead of
// streaming until the paused timer eventually fires.
func TestBindingExpiresWhenWallClockPassesDuringHostSleep(t *testing.T) {
	service, clock := newTestService(t)
	service.expiryRecheck = 10 * time.Millisecond
	issued := pair(t, service, "Phone")
	binding, principal, err := service.Bind(context.Background(), issued.Credential)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Release()
	clock.Set(principal.ValidUntil) // Days pass on the wall clock; no monotonic time does.
	select {
	case <-binding.Revoked():
	case <-time.After(5 * time.Second):
		t.Fatal("binding kept streaming after the wall clock passed its validity")
	}
	if !errors.Is(binding.Check(), ErrExpired) {
		t.Fatalf("expired binding check = %v", binding.Check())
	}
}
