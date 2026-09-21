package auth

import (
	"errors"
	"testing"
	"time"
)

func TestBrowserLifetimeExactExpiryAndNoRevival(t *testing.T) {
	created := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lifetime, err := NewBrowserLifetime(created)
	if err != nil {
		t.Fatal(err)
	}
	for _, instant := range []time.Time{created, created.Add(BrowserInactivityLifetime - time.Nanosecond)} {
		if err := lifetime.Check(instant); err != nil {
			t.Fatal("active credential rejected: ", err)
		}
	}
	if err := lifetime.Check(created.Add(BrowserInactivityLifetime)); !errors.Is(err, ErrExpired) {
		t.Fatal("inactivity boundary accepted")
	}
	if _, err := lifetime.Used(created.Add(BrowserInactivityLifetime)); !errors.Is(err, ErrExpired) {
		t.Fatal("use revived inactive credential")
	}
	for day := 1; day < 30; day++ {
		lifetime, err = lifetime.Used(created.Add(time.Duration(day) * 24 * time.Hour))
		if err != nil || !lifetime.ExpiresAt.Equal(created.Add(BrowserAbsoluteLifetime)) {
			t.Fatal("valid use changed absolute lifetime")
		}
	}
	if err := lifetime.Check(lifetime.ExpiresAt.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := lifetime.Check(lifetime.ExpiresAt); !errors.Is(err, ErrExpired) {
		t.Fatal("absolute expiry boundary accepted")
	}
	if _, err := lifetime.Used(lifetime.ExpiresAt); !errors.Is(err, ErrExpired) {
		t.Fatal("use revived absolutely expired credential")
	}
	lifetime.Revoked = true
	if _, err := lifetime.Used(lifetime.LastUsedAt); !errors.Is(err, ErrRevoked) {
		t.Fatal("use revived revoked credential")
	}
}

func TestBrowserLifetimeRejectsInvalidMetadataAndClockRollback(t *testing.T) {
	created := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	initial, _ := NewBrowserLifetime(created)
	used, _ := initial.Used(created.Add(time.Hour))
	for name, test := range map[string]struct {
		lifetime BrowserLifetime
		now      time.Time
	}{
		"zero":                     {BrowserLifetime{}, created},
		"zero clock":               {initial, time.Time{}},
		"creation in future":       {initial, created.Add(-time.Second)},
		"last use in future":       {used, created},
		"last use before creation": {BrowserLifetime{CreatedAt: created, LastUsedAt: created.Add(-time.Second), ExpiresAt: initial.ExpiresAt}, created},
		"extended expiry":          {BrowserLifetime{CreatedAt: created, LastUsedAt: created, ExpiresAt: initial.ExpiresAt.Add(time.Second)}, created},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test.lifetime.Check(test.now); !errors.Is(err, ErrInvalidLifetime) {
				t.Fatalf("invalid lifetime result: %v", err)
			}
			if _, err := test.lifetime.Used(test.now); !errors.Is(err, ErrInvalidLifetime) {
				t.Fatal("invalid state was updated")
			}
		})
	}
	for _, now := range []time.Time{time.Time{}, time.Unix(-1, 0), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)} {
		if _, err := NewBrowserLifetime(now); !errors.Is(err, ErrInvalidLifetime) {
			t.Fatal("unrepresentable lifetime accepted")
		}
	}
}

func TestPairingGrantHasTwoMinuteBoundary(t *testing.T) {
	created := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	expires := created.Add(PairingGrantLifetime)
	if err := CheckPairingGrantLifetime(created, expires, expires.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if err := CheckPairingGrantLifetime(created, expires, expires); !errors.Is(err, ErrExpired) {
		t.Fatal("expired grant accepted")
	}
	if err := CheckPairingGrantLifetime(created, expires.Add(time.Second), created); !errors.Is(err, ErrInvalidLifetime) {
		t.Fatal("extended grant lifetime accepted")
	}
	if err := CheckPairingGrantLifetime(created, expires, created.Add(-time.Second)); !errors.Is(err, ErrInvalidLifetime) {
		t.Fatal("future grant accepted after clock rollback")
	}
}
