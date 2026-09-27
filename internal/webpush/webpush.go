// Package webpush sends private Web Push notifications with the Go standard
// library only: RFC 8291 message encryption (aes128gcm), RFC 8292 VAPID
// (ES256 JWT) and an exact allow-list of push service hosts.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// PublicKeyBytes is an uncompressed P-256 point.
	PublicKeyBytes = 65
	// AuthSecretBytes is the subscription authentication secret size.
	AuthSecretBytes = 16
	// MaxEndpointBytes bounds a browser-supplied endpoint URL.
	MaxEndpointBytes = 2048
	// MaxPayloadBytes keeps the encrypted body under the 4096-byte limit
	// push services must accept (RFC 8030 section 7.2).
	MaxPayloadBytes = 3000

	recordSize     = 4096
	requestTimeout = 10 * time.Second
	jwtLifetime    = 12 * time.Hour
)

var (
	ErrEndpoint = errors.New("webpush: endpoint is not an https URL on a known push service")
	ErrKey      = errors.New("webpush: invalid subscription key")
	b64         = base64.RawURLEncoding
)

// Keys is the broker's VAPID key pair. The private scalar never leaves this
// type except through PrivateBytes for the private SQLite store; formatting
// and logging are redacted.
type Keys struct{ private *ecdsa.PrivateKey }

// GenerateKeys creates a fresh P-256 key pair from crypto/rand.
func GenerateKeys() (Keys, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	return Keys{private: key}, nil
}

// ParseKeys loads a 32-byte private scalar saved by PrivateBytes.
func ParseKeys(raw []byte) (Keys, error) {
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return Keys{}, errors.New("webpush: invalid VAPID private key")
	}
	return Keys{private: key}, nil
}

// PrivateBytes returns the 32-byte scalar for private persistence only.
func (keys Keys) PrivateBytes() ([]byte, error) {
	if keys.private == nil {
		return nil, errors.New("webpush: no VAPID key")
	}
	return keys.private.Bytes()
}

// PublicKey is the applicationServerKey the browser subscribes with.
func (keys Keys) PublicKey() string {
	public, err := keys.private.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(public)
}

func (Keys) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[REDACTED]") }
func (Keys) String() string                 { return "[REDACTED]" }
func (Keys) GoString() string               { return "webpush.Keys([REDACTED])" }
func (Keys) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (Keys) MarshalJSON() ([]byte, error) {
	return nil, errors.New("webpush: keys are not serializable")
}

// Subscription is one browser PushSubscription.
type Subscription struct {
	Endpoint string
	P256dh   []byte // 65-byte uncompressed user agent public key
	Auth     []byte // 16-byte authentication secret
}

// ValidateEndpoint is the SSRF guard. It accepts only https URLs, on port
// 443, whose host is exactly a known push service or a subdomain of one.
// Matching is by whole DNS labels, never by substring.
func ValidateEndpoint(raw string) error {
	if len(raw) == 0 || len(raw) > MaxEndpointBytes {
		return ErrEndpoint
	}
	for index := 0; index < len(raw); index++ {
		if raw[index] <= ' ' || raw[index] >= 0x7f || raw[index] == '\\' {
			return ErrEndpoint
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return ErrEndpoint
	}
	if !allowedHost(parsed.Hostname()) {
		return ErrEndpoint
	}
	return nil
}

// Exact push service hosts (Chrome/Android FCM, Firefox autopush, Safari
// and iOS Home Screen apps).
var exactHosts = map[string]bool{
	"fcm.googleapis.com":                true,
	"updates.push.services.mozilla.com": true,
	"web.push.apple.com":                true,
}

// Suffixes allow any subdomain: Apple documents *.push.apple.com; Edge on
// Windows uses per-region *.notify.windows.com (for example
// wns2-db5p.notify.windows.com).
var hostSuffixes = []string{".push.apple.com", ".notify.windows.com"}

func allowedHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			character := label[index]
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false // Uppercase, IP literals with ':', '%' escapes and '_' are all refused.
			}
		}
	}
	if exactHosts[host] {
		return true
	}
	for _, suffix := range hostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true // Labels are non-empty, so a subdomain exists before the suffix.
		}
	}
	return false
}

// Encrypt produces an RFC 8291 aes128gcm body with a fresh ephemeral key
// and salt.
func Encrypt(plaintext, userAgentPublic, authSecret []byte) ([]byte, error) {
	ephemeral, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(plaintext, userAgentPublic, authSecret, salt, ephemeral)
}

func encrypt(plaintext, userAgentPublic, authSecret, salt []byte, ephemeral *ecdh.PrivateKey) ([]byte, error) {
	if len(authSecret) != AuthSecretBytes || len(salt) != 16 || len(plaintext) > MaxPayloadBytes {
		return nil, ErrKey
	}
	remote, err := ecdh.P256().NewPublicKey(userAgentPublic)
	if err != nil {
		return nil, ErrKey
	}
	shared, err := ephemeral.ECDH(remote)
	if err != nil {
		return nil, ErrKey
	}
	serverPublic := ephemeral.PublicKey().Bytes()
	keyInfo := append(append([]byte("WebPush: info\x00"), userAgentPublic...), serverPublic...)
	ikm, err := hkdf.Key(sha256.New, shared, authSecret, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: header || AES-GCM(plaintext || 0x02 last-record delimiter).
	header := make([]byte, 0, 21+len(serverPublic))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(serverPublic)))
	header = append(header, serverPublic...)
	record := append(append([]byte(nil), plaintext...), 0x02)
	return gcm.Seal(header, nonce, record, nil), nil
}

// VAPIDAuthorization returns the RFC 8292 Authorization header value for an
// endpoint: "vapid t=<ES256 JWT>, k=<public key>".
func (keys Keys) VAPIDAuthorization(endpoint, subject string, now time.Time) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || keys.private == nil {
		return "", ErrEndpoint
	}
	claims, err := json.Marshal(map[string]any{
		"aud": "https://" + parsed.Hostname(), // Origin; the default port is omitted.
		"exp": now.Add(jwtLifetime).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", err
	}
	signingInput := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + b64.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, keys.private, digest[:])
	if err != nil {
		return "", err
	}
	// JOSE ES256 signatures are fixed-width r || s, not ASN.1.
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return "vapid t=" + signingInput + "." + b64.EncodeToString(signature) + ", k=" + keys.PublicKey(), nil
}

// NewHTTPClient returns the only client used for push delivery: no
// redirects, no proxy, a short timeout, and a dial check that refuses
// loopback, private, link-local and other non-public addresses even if a
// push host's DNS answer points there.
func NewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: publicAddressOnly}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			// ponytail: no proxy support; a host behind a mandatory proxy
			// gets no push until one is plumbed through explicitly.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: requestTimeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func publicAddressOnly(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	ip := addrPort.Addr().Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip) { // CGNAT, used by Tailscale.
		return fmt.Errorf("webpush: refusing non-public address %s", ip)
	}
	return nil
}

// Urgency values from RFC 8030 section 5.3.
const (
	UrgencyNormal = "normal"
	UrgencyHigh   = "high"
)

// Send encrypts payload for one subscription and posts it. It returns the
// push service status code; 404 and 410 mean the subscription is gone.
func Send(
	ctx context.Context, client *http.Client, keys Keys, subscription Subscription,
	payload []byte, subject string, ttl time.Duration, urgency string,
) (int, error) {
	if err := ValidateEndpoint(subscription.Endpoint); err != nil {
		return 0, err
	}
	body, err := Encrypt(payload, subscription.P256dh, subscription.Auth)
	if err != nil {
		return 0, err
	}
	authorization, err := keys.VAPIDAuthorization(subscription.Endpoint, subject, time.Now())
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, subscription.Endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, ErrEndpoint
	}
	request.Header.Set("Authorization", authorization)
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("TTL", strconv.Itoa(int(ttl.Seconds())))
	request.Header.Set("Urgency", urgency)
	response, err := client.Do(request)
	if err != nil {
		return 0, errors.New("webpush: push service unreachable") // Never echo the capability URL.
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	return response.StatusCode, nil
}
