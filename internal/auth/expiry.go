package auth

import (
	"errors"
	"time"
)

const (
	PairingGrantLifetime      = 2 * time.Minute
	BrowserAbsoluteLifetime   = 30 * 24 * time.Hour
	BrowserInactivityLifetime = 7 * 24 * time.Hour
)

var (
	ErrRevoked         = errors.New("auth: credential revoked")
	ErrExpired         = errors.New("auth: credential expired")
	ErrInvalidLifetime = errors.New("auth: invalid credential lifetime")
)

// BrowserLifetime is broker-owned metadata, never client-supplied authority.
// Revocation/expiry checks must also run for already-connected browser streams.
// This pure policy does not itself authorize an identity or persist last use.
type BrowserLifetime struct {
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	Revoked    bool
}

func NewBrowserLifetime(now time.Time) (BrowserLifetime, error) {
	if !validAuthTime(now) || !validAuthTime(now.Add(BrowserAbsoluteLifetime)) {
		return BrowserLifetime{}, ErrInvalidLifetime
	}
	now = now.UTC()
	return BrowserLifetime{CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(BrowserAbsoluteLifetime)}, nil
}

func (lifetime BrowserLifetime) Check(now time.Time) error {
	if lifetime.Revoked {
		return ErrRevoked
	}
	if !validAuthTime(now) || !validAuthTime(lifetime.CreatedAt) || !validAuthTime(lifetime.LastUsedAt) ||
		!validAuthTime(lifetime.ExpiresAt) || lifetime.CreatedAt.After(now) || lifetime.LastUsedAt.After(now) ||
		lifetime.LastUsedAt.Before(lifetime.CreatedAt) ||
		!lifetime.ExpiresAt.Equal(lifetime.CreatedAt.Add(BrowserAbsoluteLifetime)) {
		return ErrInvalidLifetime
	}
	if !now.Before(lifetime.ExpiresAt) || !now.Before(lifetime.LastUsedAt.Add(BrowserInactivityLifetime)) {
		return ErrExpired
	}
	return nil
}

// Used returns a checked copy; it never revives expired state or moves absolute
// expiry. The broker must persist/serialize this with authoritative revocation.
func (lifetime BrowserLifetime) Used(now time.Time) (BrowserLifetime, error) {
	if err := lifetime.Check(now); err != nil {
		return BrowserLifetime{}, err
	}
	lifetime.LastUsedAt = now.UTC()
	return lifetime, nil
}

func CheckPairingGrantLifetime(createdAt, expiresAt, now time.Time) error {
	if !validAuthTime(createdAt) || !validAuthTime(expiresAt) || !validAuthTime(now) || createdAt.After(now) ||
		!expiresAt.Equal(createdAt.Add(PairingGrantLifetime)) {
		return ErrInvalidLifetime
	}
	if !now.Before(expiresAt) {
		return ErrExpired
	}
	return nil
}

// Restrict to nonnegative, representable UTC timestamps before SQLite/wire
// conversion. Clock rollback behind creation/last-use fails closed above.
func validAuthTime(value time.Time) bool {
	return !value.IsZero() && !value.Before(time.Unix(0, 0)) && value.UTC().Year() <= 9999
}
