package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
)

const SecretBytes = 32

var (
	ErrInvalidSecret       = errors.New("auth: invalid secret")
	ErrInvalidDigest       = errors.New("auth: invalid secret digest")
	ErrRandomness          = errors.New("auth: secure random generation failed")
	errSecretSerialization = errors.New("auth: secret requires an explicit transport conversion")
)

// Secret is an independent 256-bit grant, poll secret, or browser credential.
// Ordinary formatting/logging is redacted. TransportValue is intentionally
// explicit: its result must not enter logs, URLs (except the pairing fragment),
// browser persistence, or SQLite. Go copies are not a zeroization guarantee.
type Secret struct {
	// Indirection prevents fmt's unexported-container-field fallback from
	// recursively dumping an embedded byte array. Material is immutable after
	// construction and no public method returns this pointer.
	value *[SecretBytes]byte
}

// NewSecret uses the operating system's cryptographic random source. There is
// no configurable seed, UUID-derived credential, or fallback on random failure.
func NewSecret() (Secret, error) { return generateSecret(rand.Reader) }

func generateSecret(source io.Reader) (Secret, error) {
	if source == nil {
		return Secret{}, ErrRandomness
	}
	secret := Secret{value: new([SecretBytes]byte)}
	if _, err := io.ReadFull(source, secret.value[:]); err != nil {
		return Secret{}, ErrRandomness // Never return partially generated material.
	}
	return secret, nil
}

// ParseSecret accepts only the one canonical, unpadded base64url representation.
// Neither malformed input nor a credential is included in returned errors.
func ParseSecret(value string) (Secret, error) {
	if len(value) != base64.RawURLEncoding.EncodedLen(SecretBytes) {
		return Secret{}, ErrInvalidSecret
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != SecretBytes || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return Secret{}, ErrInvalidSecret
	}
	secret := Secret{value: new([SecretBytes]byte)}
	copy(secret.value[:], decoded)
	return secret, nil
}

func (secret Secret) TransportValue() (string, error) {
	if secret.value == nil {
		return "", ErrInvalidSecret
	}
	return base64.RawURLEncoding.EncodeToString(secret.value[:]), nil
}

func (secret Secret) Hash() (Digest, error) {
	if secret.value == nil {
		return Digest{}, ErrInvalidSecret
	}
	value := sha256.Sum256(secret.value[:])
	return Digest{value: &value}, nil
}

func (Secret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[REDACTED]") }
func (Secret) String() string                 { return "[REDACTED]" }
func (Secret) GoString() string               { return "auth.Secret([REDACTED])" }
func (Secret) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (Secret) MarshalJSON() ([]byte, error)   { return nil, errSecretSerialization }
func (Secret) MarshalText() ([]byte, error)   { return nil, errSecretSerialization }

// Digest is the storage representation, not a bearer credential. Its zero value
// is invalid. Bytes returns a copy for a parameterized SQLite BLOB binding.
type Digest struct {
	value *[sha256.Size]byte
}

func DigestFromBytes(value []byte) (Digest, error) {
	if len(value) != sha256.Size {
		return Digest{}, ErrInvalidDigest
	}
	digest := Digest{value: new([sha256.Size]byte)}
	copy(digest.value[:], value)
	return digest, nil
}

func (digest Digest) Bytes() ([]byte, error) {
	if digest.value == nil {
		return nil, ErrInvalidDigest
	}
	return append([]byte(nil), digest.value[:]...), nil
}

func (digest Digest) Equal(other Digest) bool {
	if digest.value == nil || other.value == nil {
		return false
	}
	return subtle.ConstantTimeCompare(digest.value[:], other.value[:]) == 1
}

func (Digest) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[REDACTED HASH]") }
func (Digest) String() string                 { return "[REDACTED HASH]" }
func (Digest) GoString() string               { return "auth.Digest([REDACTED HASH])" }
func (Digest) LogValue() slog.Value           { return slog.StringValue("[REDACTED HASH]") }
