package auth

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const (
	PendingPairingLifetime = 5 * time.Minute
	MaxOutstandingGrants   = 8
	MaxPendingPairings     = 4
	MaxFailedPolls         = 5
	MaxDevices             = 64
	RedeemAttemptsPerMin   = 30
	PollAttemptsPerMin     = 120
	// Last use is persisted at this resolution so authenticated reads do not
	// become one SQLite write each; it only shortens inactivity by <1 minute.
	lastUseResolution = time.Minute
	// Revocation waits this long for bound live streams to close before it
	// reports incomplete cleanup instead of success.
	defaultCleanupTimeout = 5 * time.Second
	// Timers pause while the host sleeps; live bindings recheck the wall
	// clock at this interval so sleep cannot extend their validity.
	defaultExpiryRecheck = time.Minute
)

var (
	ErrUnauthorized   = errors.New("auth: credential is not valid")
	ErrPairingExpired = errors.New("auth: pairing is unknown, used, or expired")
	ErrPollSecret     = errors.New("auth: poll secret does not match")
	ErrRateLimited    = errors.New("auth: attempt rate limit reached")
	ErrCapacity       = errors.New("auth: pairing or device capacity reached")
	ErrInvalidLabel   = errors.New("auth: device label is invalid")
	ErrApproved       = errors.New("auth: pending pairing was already approved")
	ErrDeviceMissing  = errors.New("auth: device does not exist")
	ErrCleanup        = errors.New("auth: revocation is durable but live stream cleanup did not complete")
)

// ServiceConfig injects persistence, time and randomness. Tests use a fixed
// clock; production uses time.Now and crypto/rand.
type ServiceConfig struct {
	Store  *store.Store
	Now    func() time.Time
	Random io.Reader
}

// Service is the broker-owned pairing and browser-credential authority. It
// keeps revocations fail-closed in memory even when persistence fails and owns
// the live-stream bindings that revocation, logout and expiry must close.
type Service struct {
	store  *store.Store
	now    func() time.Time
	random io.Reader

	mu                 sync.Mutex
	revokedDevices     map[string]struct{}
	revokedCredentials map[string]struct{}
	bindings           map[*Binding]struct{}
	redeems            rateWindow
	polls              rateWindow
	cleanupTimeout     time.Duration
	expiryRecheck      time.Duration
}

// Principal is the broker-resolved identity for one valid credential.
type Principal struct {
	DeviceID     string
	DeviceLabel  string
	CredentialID string
	ExpiresAt    time.Time
	// ValidUntil is the earlier of absolute and inactivity expiry at the time
	// of resolution. A live binding never outlives it.
	ValidUntil time.Time
}

// PollResult carries a credential only for the one approved poll.
type PollResult struct {
	Approved    bool
	DeviceID    string
	DeviceLabel string
	Credential  string
	ExpiresAt   time.Time
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("auth: service store must not be nil")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Service{
		store: config.Store, now: config.Now, random: config.Random,
		revokedDevices: make(map[string]struct{}), revokedCredentials: make(map[string]struct{}),
		bindings:       make(map[*Binding]struct{}),
		redeems:        rateWindow{limit: RedeemAttemptsPerMin, span: time.Minute},
		polls:          rateWindow{limit: PollAttemptsPerMin, span: time.Minute},
		cleanupTimeout: defaultCleanupTimeout,
		expiryRecheck:  defaultExpiryRecheck,
	}, nil
}

// CreateGrant returns a fresh two-minute grant. Its transport value is
// returned only to the restricted local CLI and never persisted.
func (service *Service) CreateGrant(ctx context.Context) (string, time.Time, error) {
	now := service.clock()
	secret, err := generateSecret(service.random)
	if err != nil {
		return "", time.Time{}, err
	}
	hash, err := digestBytes(secret)
	if err != nil {
		return "", time.Time{}, err
	}
	id, err := newUUID(service.random)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := now.Add(PairingGrantLifetime)
	if err := service.store.CreatePairingGrant(ctx, store.PairingGrant{
		ID: id, Hash: hash, CreatedAt: now, ExpiresAt: expires,
	}, MaxOutstandingGrants); err != nil {
		return "", time.Time{}, mapStoreError(err)
	}
	value, err := secret.TransportValue()
	return value, expires, err
}

// Redeem atomically consumes a grant and creates a pending pairing with an
// independent poll secret and a human comparison code.
func (service *Service) Redeem(ctx context.Context, grant, label string) (protocol.HTTPPairRedeemResponse, error) {
	now := service.clock()
	if !service.allow(&service.redeems, now) {
		return protocol.HTTPPairRedeemResponse{}, ErrRateLimited
	}
	if protocol.ValidateDeviceLabel(label) != nil {
		return protocol.HTTPPairRedeemResponse{}, ErrInvalidLabel
	}
	parsed, err := ParseSecret(grant)
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, ErrPairingExpired
	}
	grantHash, err := digestBytes(parsed)
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, err
	}
	pollSecret, err := generateSecret(service.random)
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, err
	}
	pollHash, err := digestBytes(pollSecret)
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, err
	}
	id, err := newUUID(service.random)
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, err
	}
	code, err := verificationCode(service.random)
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, err
	}
	expires := now.Add(PendingPairingLifetime)
	if err := service.store.RedeemPairingGrant(ctx, grantHash, store.PendingPairing{
		ID: id, PollHash: pollHash, VerificationCode: code, DeviceLabel: label,
		CreatedAt: now, ExpiresAt: expires,
	}, MaxPendingPairings); err != nil {
		return protocol.HTTPPairRedeemResponse{}, mapStoreError(err)
	}
	pollValue, err := pollSecret.TransportValue()
	if err != nil {
		return protocol.HTTPPairRedeemResponse{}, err
	}
	return protocol.HTTPPairRedeemResponse{
		PendingID: id, PollSecret: pollValue, VerificationCode: code, ExpiresAt: formatTime(expires),
	}, nil
}

func (service *Service) Pending(ctx context.Context) ([]store.PendingPairing, error) {
	return service.store.ListPendingPairings(ctx, service.clock(), protocol.MaxAdminPendingPairings)
}

// Approve is reachable only through restricted local administration.
func (service *Service) Approve(ctx context.Context, pendingID string) error {
	return mapStoreError(service.store.ApprovePendingPairing(ctx, pendingID, service.clock()))
}

// Poll requires the independent poll secret. The approved poll atomically
// creates the device and credential; the plaintext exists only in this result.
func (service *Service) Poll(ctx context.Context, pendingID, pollSecret string) (PollResult, error) {
	now := service.clock()
	if !service.allow(&service.polls, now) {
		return PollResult{}, ErrRateLimited
	}
	parsed, err := ParseSecret(pollSecret)
	if err != nil {
		return PollResult{}, ErrPollSecret
	}
	pollHash, err := digestBytes(parsed)
	if err != nil {
		return PollResult{}, err
	}
	credential, err := generateSecret(service.random)
	if err != nil {
		return PollResult{}, err
	}
	credentialHash, err := digestBytes(credential)
	if err != nil {
		return PollResult{}, err
	}
	lifetime, err := NewBrowserLifetime(now)
	if err != nil {
		return PollResult{}, err
	}
	deviceID, err := newUUID(service.random)
	if err != nil {
		return PollResult{}, err
	}
	sessionID, err := newUUID(service.random)
	if err != nil {
		return PollResult{}, err
	}
	device, approved, err := service.store.CompletePendingPairing(ctx, pendingID, pollHash, now,
		store.Device{ID: deviceID, CreatedAt: now, LastUsedAt: now},
		store.BrowserSession{
			ID: sessionID, DeviceID: deviceID, CredentialHash: credentialHash,
			CreatedAt: lifetime.CreatedAt, LastUsedAt: lifetime.LastUsedAt, ExpiresAt: lifetime.ExpiresAt,
		},
		store.PairingLimits{FailedPolls: MaxFailedPolls, Devices: MaxDevices, CredentialIdle: BrowserInactivityLifetime})
	if err != nil {
		return PollResult{}, mapStoreError(err)
	}
	if !approved {
		return PollResult{}, nil
	}
	value, err := credential.TransportValue()
	if err != nil {
		return PollResult{}, err
	}
	return PollResult{
		Approved: true, DeviceID: device.ID, DeviceLabel: device.Label,
		Credential: value, ExpiresAt: lifetime.ExpiresAt,
	}, nil
}

// Authenticate resolves a raw credential for one request. It checks in-memory
// and durable revocation, absolute and inactivity expiry, and clock rollback.
func (service *Service) Authenticate(ctx context.Context, credential string) (Principal, error) {
	principal, err := service.resolve(ctx, credential)
	if err != nil {
		return Principal{}, err
	}
	service.mu.Lock()
	err = service.revokedLocked(principal)
	service.mu.Unlock()
	return principal, err
}

func (service *Service) resolve(ctx context.Context, credential string) (Principal, error) {
	now := service.clock()
	parsed, err := ParseSecret(credential)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	hash, err := digestBytes(parsed)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	session, device, err := service.store.LookupCredential(ctx, hash)
	if errors.Is(err, store.ErrCredentialMissing) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, err
	}
	principal := Principal{
		DeviceID: device.ID, DeviceLabel: device.Label, CredentialID: session.ID, ExpiresAt: session.ExpiresAt,
	}
	if session.RevokedAt != nil || device.RevokedAt != nil {
		return Principal{}, ErrRevoked
	}
	lifetime := BrowserLifetime{CreatedAt: session.CreatedAt, LastUsedAt: session.LastUsedAt, ExpiresAt: session.ExpiresAt}
	if err := lifetime.Check(now); err != nil {
		return Principal{}, ErrUnauthorized
	}
	if now.Sub(session.LastUsedAt) >= lastUseResolution {
		if err := service.store.TouchCredential(ctx, session.ID, now); err != nil {
			return Principal{}, err
		}
		session.LastUsedAt = now
	}
	principal.ValidUntil = session.LastUsedAt.Add(BrowserInactivityLifetime)
	if session.ExpiresAt.Before(principal.ValidUntil) {
		principal.ValidUntil = session.ExpiresAt
	}
	return principal, nil
}

func (service *Service) revokedLocked(principal Principal) error {
	_, device := service.revokedDevices[principal.DeviceID]
	_, credential := service.revokedCredentials[principal.CredentialID]
	if device || credential {
		return ErrRevoked
	}
	return nil
}

// Bind authenticates a credential and registers a live-stream binding in the
// same critical section that revocation uses, so a stream cannot be bound
// after a concurrent revocation has already selected its targets.
func (service *Service) Bind(ctx context.Context, credential string) (*Binding, Principal, error) {
	principal, err := service.resolve(ctx, credential)
	if err != nil {
		return nil, Principal{}, err
	}
	binding := &Binding{
		service: service, principal: principal,
		revoked: make(chan struct{}), released: make(chan struct{}),
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := service.revokedLocked(principal); err != nil {
		return nil, Principal{}, err
	}
	service.bindings[binding] = struct{}{}
	service.armExpiryLocked(binding, principal.ValidUntil.Sub(service.now()))
	return binding, principal, nil
}

// armExpiryLocked ends the binding once left elapses on the monotonic timer
// or the wall clock reaches ValidUntil, whichever is first. Timers pause while
// the host sleeps, so the wall clock is rechecked at least every expiryRecheck.
func (service *Service) armExpiryLocked(binding *Binding, left time.Duration) {
	step := max(min(left, service.expiryRecheck), 0)
	binding.timer = time.AfterFunc(step, func() {
		service.mu.Lock()
		defer service.mu.Unlock()
		select {
		case <-binding.revoked:
			return
		default:
		}
		left := min(left-step, binding.principal.ValidUntil.Sub(service.now()))
		if left <= 0 {
			binding.revokeLocked(ErrExpired)
			return
		}
		service.armExpiryLocked(binding, left)
	})
}

// Logout revokes only the presented credential and closes its streams. It
// never terminates a managed child or affects other devices.
func (service *Service) Logout(ctx context.Context, credential string) error {
	principal, err := service.Authenticate(ctx, credential)
	if err != nil {
		return err
	}
	service.mu.Lock()
	service.revokedCredentials[principal.CredentialID] = struct{}{}
	targets := service.revokeBindingsLocked(func(binding *Binding) bool {
		return binding.principal.CredentialID == principal.CredentialID
	}, ErrRevoked)
	service.mu.Unlock()
	persistErr := service.store.RevokeCredential(ctx, principal.CredentialID, service.clock())
	return errors.Join(persistErr, waitReleased(ctx, targets, service.cleanupTimeout))
}

// RevokeDevice is the broker-wide revocation barrier. New authentication and
// bindings fail before persistence starts; the in-memory revocation remains
// even if persistence fails. It reports success only after the revocation is
// durable and every bound stream has released.
func (service *Service) RevokeDevice(ctx context.Context, deviceID string) (int, error) {
	if !validUUIDText(deviceID) {
		return 0, ErrDeviceMissing
	}
	service.mu.Lock()
	_, previouslyRevoked := service.revokedDevices[deviceID]
	service.revokedDevices[deviceID] = struct{}{}
	targets := service.revokeBindingsLocked(func(binding *Binding) bool {
		return binding.principal.DeviceID == deviceID
	}, ErrRevoked)
	service.mu.Unlock()
	credentials, err := service.store.RevokeDevice(ctx, deviceID, service.clock())
	if errors.Is(err, store.ErrDeviceMissing) {
		if !previouslyRevoked {
			service.mu.Lock()
			delete(service.revokedDevices, deviceID) // Nothing was bound to an unknown device.
			service.mu.Unlock()
		}
		return 0, ErrDeviceMissing
	}
	service.mu.Lock()
	for _, id := range credentials {
		service.revokedCredentials[id] = struct{}{}
	}
	service.mu.Unlock()
	return len(targets), errors.Join(err, waitReleased(ctx, targets, service.cleanupTimeout))
}

func (service *Service) Devices(ctx context.Context) ([]store.Device, error) {
	return service.store.ListDevices(ctx, service.clock(), BrowserInactivityLifetime, protocol.MaxAdminDevices)
}

func (service *Service) revokeBindingsLocked(match func(*Binding) bool, reason error) []*Binding {
	var targets []*Binding
	for binding := range service.bindings {
		if match(binding) {
			binding.revokeLocked(reason)
			targets = append(targets, binding)
		}
	}
	return targets
}

func waitReleased(ctx context.Context, bindings []*Binding, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, binding := range bindings {
		select {
		case <-binding.released:
		case <-timer.C:
			return ErrCleanup
		case <-ctx.Done():
			return errors.Join(ErrCleanup, ctx.Err())
		}
	}
	return nil
}

// clock returns the current time at SQLite's millisecond resolution, so every
// reported expiry equals the persisted one.
func (service *Service) clock() time.Time {
	return service.now().UTC().Truncate(time.Millisecond)
}

func (service *Service) allow(window *rateWindow, now time.Time) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	return window.allow(now)
}

// Binding ties one live stream to one credential. Check is safe from any
// goroutine, including the session actor immediately before encoding input.
type Binding struct {
	service   *Service
	principal Principal
	timer     *time.Timer // Guarded by service.mu.
	revoked   chan struct{}
	released  chan struct{}
	err       error // Written under service.mu before revoked closes.
	release   sync.Once
}

func (binding *Binding) Principal() Principal { return binding.principal }

// Revoked closes when revocation, logout or expiry ends the binding.
func (binding *Binding) Revoked() <-chan struct{} { return binding.revoked }

// Check authorizes one action. It fails after revocation or at expiry even if
// the expiry timer has not fired yet.
func (binding *Binding) Check() error {
	select {
	case <-binding.revoked:
		return binding.err
	default:
	}
	if !binding.service.now().Before(binding.principal.ValidUntil) {
		return ErrExpired
	}
	return nil
}

// Release is called once by the stream owner after it has closed the viewer
// and connection. It unblocks a revocation waiting for honest cleanup.
func (binding *Binding) Release() {
	binding.release.Do(func() {
		binding.service.mu.Lock()
		delete(binding.service.bindings, binding)
		binding.revokeLocked(ErrUnauthorized)
		binding.timer.Stop() // Under mu: the expiry callback re-arms timer.
		binding.service.mu.Unlock()
		close(binding.released)
	})
}

func (binding *Binding) revokeLocked(reason error) {
	select {
	case <-binding.revoked:
	default:
		binding.err = reason
		close(binding.revoked)
	}
}

type rateWindow struct {
	start time.Time
	count int
	limit int
	span  time.Duration
}

// ponytail: one broker-wide fixed window per attempt kind; per-source limits
// belong in the gateway, which sees network peers.
func (window *rateWindow) allow(now time.Time) bool {
	if now.Before(window.start) || !now.Before(window.start.Add(window.span)) {
		window.start, window.count = now, 0
	}
	if window.count >= window.limit {
		return false
	}
	window.count++
	return true
}

func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrPairingInvalid), errors.Is(err, store.ErrPendingMissing):
		return ErrPairingExpired
	case errors.Is(err, store.ErrPendingSecret):
		return ErrPollSecret
	case errors.Is(err, store.ErrPairingCapacity), errors.Is(err, store.ErrDeviceCapacity):
		return ErrCapacity
	case errors.Is(err, store.ErrPendingApproved):
		return ErrApproved
	case errors.Is(err, store.ErrDeviceMissing):
		return ErrDeviceMissing
	default:
		return err
	}
}

func digestBytes(secret Secret) ([]byte, error) {
	digest, err := secret.Hash()
	if err != nil {
		return nil, err
	}
	return digest.Bytes()
}

// verificationCode is uniform over 000000..999999 by rejection sampling.
func verificationCode(random io.Reader) (string, error) {
	const limit = 4_294_000_000 // Largest multiple of 1e6 below 2^32.
	var buffer [4]byte
	for range 64 {
		if _, err := io.ReadFull(random, buffer[:]); err != nil {
			return "", ErrRandomness
		}
		if value := binary.BigEndian.Uint32(buffer[:]); value < limit {
			code := value % 1_000_000
			return fmt.Sprintf("%03d-%03d", code/1000, code%1000), nil
		}
	}
	return "", ErrRandomness
}

func newUUID(random io.Reader) (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", ErrRandomness
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded[:]), nil
}

func validUUIDText(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
		} else if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
