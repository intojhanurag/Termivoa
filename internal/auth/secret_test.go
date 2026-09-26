package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestSecretCanonicalTransportHashAndStorage(t *testing.T) {
	material := bytes.Repeat([]byte{0x42}, SecretBytes)
	source := bytes.NewReader(append(append([]byte(nil), material...), 0xff))
	secret, err := generateSecret(source)
	if err != nil || source.Len() != 1 {
		t.Fatalf("generation consumed wrong number of bytes: remaining=%d error=%v", source.Len(), err)
	}
	value, err := secret.TransportValue()
	if err != nil || value != base64.RawURLEncoding.EncodeToString(material) || len(value) != 43 {
		t.Fatal("incorrect canonical transport format")
	}
	parsed, err := ParseSecret(value)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := secret.Hash()
	if err != nil {
		t.Fatal(err)
	}
	parsedDigest, err := parsed.Hash()
	if err != nil || !digest.Equal(parsedDigest) {
		t.Fatal("canonical parse changed hash")
	}
	stored, err := digest.Bytes()
	want := sha256.Sum256(material)
	if err != nil || !bytes.Equal(stored, want[:]) || bytes.Equal(stored, material) {
		t.Fatal("storage must be SHA-256 of the decoded secret bytes")
	}
	loaded, err := DigestFromBytes(stored)
	if err != nil || !loaded.Equal(digest) {
		t.Fatal("digest did not round-trip storage")
	}
	stored[0] ^= 0xff
	if !loaded.Equal(digest) {
		t.Fatal("digest aliases caller storage")
	}
	modified, _ := DigestFromBytes(stored)
	if modified.Equal(digest) {
		t.Fatal("different hashes compared equal")
	}
}

func TestSecretsAreIndependentAndMalformedValuesFailClosed(t *testing.T) {
	seen := make(map[string]bool)
	for range 64 {
		secret, err := NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		value, err := secret.TransportValue()
		if err != nil || seen[value] {
			t.Fatal("random generation failed or repeated a credential")
		}
		seen[value] = true
	}
	for _, value := range []string{
		"", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("A", 42) + "B",
		strings.Repeat("A", 42) + "=", strings.Repeat("A", 42) + "/", strings.Repeat("A", 42) + "+",
		strings.Repeat("A", 41) + "\r\n", strings.Repeat("A", 40) + "界", strings.Repeat("A", 42) + "\x00",
	} {
		secret, err := ParseSecret(value)
		if !errors.Is(err, ErrInvalidSecret) || secret.value != nil {
			t.Fatal("malformed secret accepted")
		}
	}
	for _, source := range []io.Reader{nil, bytes.NewReader(make([]byte, 31)), failingRandom{}} {
		secret, err := generateSecret(source)
		if !errors.Is(err, ErrRandomness) || secret.value != nil {
			t.Fatal("failed random source returned usable or partial material")
		}
	}
	var secret Secret
	if _, err := secret.TransportValue(); !errors.Is(err, ErrInvalidSecret) {
		t.Fatal("zero secret transport accepted")
	}
	if _, err := secret.Hash(); !errors.Is(err, ErrInvalidSecret) {
		t.Fatal("zero secret hash accepted")
	}
	var digest Digest
	if digest.Equal(digest) {
		t.Fatal("invalid zero digests compare as authorized")
	}
	if _, err := digest.Bytes(); !errors.Is(err, ErrInvalidDigest) {
		t.Fatal("invalid zero digest serialized")
	}
	for _, n := range []int{0, 31, 33} {
		if _, err := DigestFromBytes(make([]byte, n)); !errors.Is(err, ErrInvalidDigest) {
			t.Fatal("invalid digest length accepted")
		}
	}
}

type failingRandom struct{}

func (failingRandom) Read([]byte) (int, error) { return 0, errors.New("synthetic random failure") }

func TestSecretFormattingAndSerializationNeverExposeMaterial(t *testing.T) {
	secret, err := generateSecret(bytes.NewReader(bytes.Repeat([]byte{0x42}, SecretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := secret.TransportValue()
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%b", "%o"} {
		formatted := fmt.Sprintf(format, secret)
		if formatted != "[REDACTED]" {
			t.Fatal("format leaked secret material")
		}
	}
	if _, err := json.Marshal(struct{ Credential Secret }{secret}); !errors.Is(err, errSecretSerialization) {
		t.Fatal("secret silently serialized as JSON")
	}
	if _, err := secret.MarshalText(); !errors.Is(err, errSecretSerialization) {
		t.Fatal("secret silently serialized as text")
	}
	var log bytes.Buffer
	digest, _ := secret.Hash()
	slog.New(slog.NewJSONHandler(&log, nil)).Info("synthetic", "credential", secret, "digest", digest)
	if strings.Contains(log.String(), value) || !strings.Contains(log.String(), "[REDACTED]") || !strings.Contains(log.String(), "[REDACTED HASH]") {
		t.Fatal("structured log did not redact secret/hash")
	}
	// fmt cannot call methods on an unexported containing field. An embedded
	// byte array therefore leaks despite Secret.String/GoString/LogValue; the
	// opaque backing pointer must keep this fallback from inspecting material.
	container := struct {
		credential Secret
		hash       Digest
	}{secret, digest}
	for _, format := range []string{"%v", "%+v", "%#v", "%d"} {
		assertNoSyntheticSecret(t, fmt.Sprintf(format, container), value)
	}
	log.Reset()
	slog.New(slog.NewTextHandler(&log, nil)).Info("synthetic", "record", container)
	assertNoSyntheticSecret(t, log.String(), value)
}

func assertNoSyntheticSecret(t *testing.T, formatted, token string) {
	t.Helper()
	for _, representation := range []string{token, strings.Repeat("B", 32), "4242424242", "66 66 66", "0x42, 0x42, 0x42"} {
		if strings.Contains(formatted, representation) {
			t.Fatal("containing-field formatting exposed secret material")
		}
	}
}
