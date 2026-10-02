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
		rollback bool // valid metadata, host clock behind it
	}{
		"zero":                     {BrowserLifetime{}, created, false},
		"zero clock":               {initial, time.Time{}, true},
		"creation in future":       {initial, created.Add(-time.Second), true},
		"last use in future":       {used, created, true},
		"last use before creation": {BrowserLifetime{CreatedAt: created, LastUsedAt: created.Add(-time.Second), ExpiresAt: initial.ExpiresAt}, created, false},
		"extended expiry":          {BrowserLifetime{CreatedAt: created, LastUsedAt: created, ExpiresAt: initial.ExpiresAt.Add(time.Second)}, created, false},
	} {
		t.Run(name, func(t *testing.T) {
			err := test.lifetime.Check(test.now)
			if !errors.Is(err, ErrInvalidLifetime) {
				t.Fatalf("invalid lifetime result: %v", err)
			}
			if errors.Is(err, ErrClockRollback) != test.rollback {
				t.Fatalf("clock rollback = %v, want %v", err, test.rollback)
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

// Used is a test helper: a checked copy with LastUsedAt moved to now.
func (lifetime BrowserLifetime) Used(now time.Time) (BrowserLifetime, error) {
	if err := lifetime.Check(now); err != nil {
		return BrowserLifetime{}, err
	}
	lifetime.LastUsedAt = now.UTC()
	return lifetime, nil
}
