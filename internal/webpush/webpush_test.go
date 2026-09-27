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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

func decode(t *testing.T, value string) []byte {
	t.Helper()
	data, err := b64.DecodeString(strings.Join(strings.Fields(value), ""))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRFC8291AppendixA reproduces the RFC 8291 example message exactly.
func TestRFC8291AppendixA(t *testing.T) {
	serverPrivate, err := ecdh.P256().NewPrivateKey(decode(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	if got := b64.EncodeToString(serverPrivate.PublicKey().Bytes()); got !=
		"BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8" {
		t.Fatalf("as_public = %s", got)
	}
	body, err := encrypt(
		decode(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24"),
		decode(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		decode(t, "BTBZMqHH6r4Tts7J_aSIgg"),
		decode(t, "DGv6ra1nlYgDCS1FRnbzlw"),
		serverPrivate,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := decode(t, `DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z 9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml
		mlMoZIIgDll6e3vCYLocInmYWAmS6Tlz AC8wEqKK6PBru3jl7A8`)
	want = append(want, decode(t, `8pfeW0KbunFT06SuDKoJH9Ql87S1QUrd irN6GcG7sFz1y1sqLgVi1VhjVkHsUoEs bI_0LpXMuGvnzQ`)...)
	if !bytes.Equal(body, want) {
		t.Fatalf("message =\n%s\nwant\n%s", b64.EncodeToString(body), b64.EncodeToString(want))
	}
	// Section 5 prints the same message as one value.
	if b64.EncodeToString(body) != "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN" {
		t.Fatal("message differs from RFC 8291 section 5")
	}
}

// decryptForTest is the user agent side of RFC 8291, used to prove what a
// phone would see.
func decryptForTest(t *testing.T, body []byte, userAgent *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	salt, keyLength := body[:16], int(body[20])
	serverPublic := body[21 : 21+keyLength]
	remote, err := ecdh.P256().NewPublicKey(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := userAgent.ECDH(remote)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), userAgent.PublicKey().Bytes()...), serverPublic...)
	ikm, _ := hkdf.Key(sha256.New, shared, authSecret, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+keyLength:], nil)
	if err != nil || plain[len(plain)-1] != 0x02 {
		t.Fatalf("decrypt: %v", err)
	}
	return plain[:len(plain)-1]
}

var vapidHeader = regexp.MustCompile(`^vapid t=([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+), k=([A-Za-z0-9_-]{87})$`)

func TestVAPIDHeaderVerifiesWithThePublicKey(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	header, err := keys.VAPIDAuthorization("https://web.push.apple.com:443/QGx?y=1", Subject, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := vapidHeader.FindStringSubmatch(header)
	if parts == nil || parts[4] != keys.PublicKey() {
		t.Fatalf("header = %q", header)
	}
	var jose map[string]string
	var claims map[string]any
	if json.Unmarshal(decode(t, parts[1]), &jose) != nil || jose["alg"] != "ES256" || jose["typ"] != "JWT" {
		t.Fatalf("jose = %v", jose)
	}
	if json.Unmarshal(decode(t, parts[2]), &claims) != nil || claims["aud"] != "https://web.push.apple.com" ||
		claims["sub"] != Subject || claims["exp"] != float64(now.Add(12*time.Hour).Unix()) {
		t.Fatalf("claims = %v", claims)
	}
	signature := decode(t, parts[3])
	public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), decode(t, parts[4]))
	if err != nil || len(signature) != 64 {
		t.Fatalf("key %v, signature %d bytes", err, len(signature))
	}
	digest := sha256.Sum256([]byte(parts[1] + "." + parts[2]))
	r, s := new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(public, digest[:], r, s) {
		t.Fatal("VAPID signature does not verify")
	}
	digest[0] ^= 1
	if ecdsa.Verify(public, digest[:], r, s) {
		t.Fatal("tampered JWT verified")
	}
}

func TestKeysRoundTripAndRedact(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := keys.PrivateBytes()
	if err != nil || len(raw) != 32 {
		t.Fatalf("private bytes %d, %v", len(raw), err)
	}
	parsed, err := ParseKeys(raw)
	if err != nil || parsed.PublicKey() != keys.PublicKey() {
		t.Fatalf("parse = %v", err)
	}
	printed := fmt.Sprintf("%v %+v %#v %s", keys, keys, keys, keys)
	if strings.Contains(printed, b64.EncodeToString(raw)) || strings.Count(printed, "REDACTED") != 4 {
		t.Fatalf("keys printed as %q", printed)
	}
	if _, err := json.Marshal(keys); err == nil {
		t.Fatal("keys marshaled to JSON")
	}
	if _, err := ParseKeys(make([]byte, 32)); err == nil {
		t.Fatal("zero scalar accepted")
	}
}

func TestValidateEndpointAllowsOnlyKnownPushServices(t *testing.T) {
	for _, endpoint := range []string{
		"https://fcm.googleapis.com/fcm/send/abc:APA91b",
		"https://fcm.googleapis.com/wp/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAA",
		"https://web.push.apple.com/QGx",
		"https://api.push.apple.com/3/device/x",
		"https://wns2-db5p.notify.windows.com/w/?token=BQYAAA",
		"https://web.push.apple.com:443/QGx",
	} {
		if err := ValidateEndpoint(endpoint); err != nil {
			t.Errorf("%s rejected", endpoint)
		}
	}
	for _, endpoint := range []string{
		"", "http://fcm.googleapis.com/fcm/send/x", "https://fcm.googleapis.com:8443/x",
		"https://evil.example/fcm.googleapis.com", "https://fcm.googleapis.com.evil.example/x",
		"https://evilfcm.googleapis.com/x", "https://googleapis.com/x", "https://push.apple.com/x",
		"https://evilpush.apple.com/x", "https://notify.windows.com/x", "https://x.notify.windows.com.evil/x",
		"https://FCM.googleapis.com/x", "https://fcm.googleapis.com./x", "https://user@fcm.googleapis.com/x",
		"https://evil.example#@fcm.googleapis.com/x", "https://127.0.0.1/x", "https://[::1]/x",
		"https://169.254.169.254/latest", "https://localhost/x", "https://fcm%2egoogleapis.com/x",
		"https://a..push.apple.com/x", "https://-a.push.apple.com/x", "https://a_b.push.apple.com/x",
		"https://fcm.googleapis.com/x y", "https://fcm.googleapis.com\\@evil.example/x",
		"https://fcm.googleapis.com/" + strings.Repeat("a", MaxEndpointBytes), "javascript:alert(1)",
		"https:fcm.googleapis.com/x", "//fcm.googleapis.com/x",
	} {
		if err := ValidateEndpoint(endpoint); err == nil {
			t.Errorf("%q accepted", endpoint)
		}
	}
}

func TestHTTPClientRefusesRedirectsAndPrivateAddresses(t *testing.T) {
	client := NewHTTPClient()
	if client.CheckRedirect(nil, nil) != http.ErrUseLastResponse || client.Timeout <= 0 || client.Timeout > 30*time.Second {
		t.Fatal("client follows redirects or has no short timeout")
	}
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("client uses a proxy")
	}
	for _, address := range []string{"127.0.0.1:443", "10.0.0.1:443", "192.168.1.1:443", "172.16.0.1:443",
		"169.254.169.254:443", "100.100.100.100:443", "[::1]:443", "[fd00::1]:443", "[fe80::1]:443", "0.0.0.0:443",
		"[::ffff:127.0.0.1]:443"} {
		if publicAddressOnly("tcp", address, nil) == nil {
			t.Errorf("%s allowed", address)
		}
	}
	for _, address := range []string{"142.250.1.1:443", "[2607:f8b0::1]:443"} {
		if err := publicAddressOnly("tcp", address, nil); err != nil {
			t.Errorf("%s refused: %v", address, err)
		}
	}
}

func TestEventMessagesCarryNamesOnly(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	for _, test := range []struct {
		event Event
		want  Message
	}{
		{Event{SessionID: id, SessionTitle: "claude", ProjectName: "web", Kind: KindWaiting},
			Message{Title: "claude is waiting for you", Body: "web · tap to answer", SessionID: id, Kind: KindWaiting}},
		{Event{SessionID: id, SessionTitle: "web", Kind: KindDone, Duration: 12*time.Minute + 20*time.Second},
			Message{Title: "web is done · your turn", Body: "Worked for 12 min", SessionID: id, Kind: KindDone}},
		{Event{SessionID: id, SessionTitle: " \x1b]0;x\x07\n", Kind: KindDone, Duration: time.Second},
			Message{Title: "]0;x is done · your turn", Body: "Worked for 1 min", SessionID: id, Kind: KindDone}},
		{Event{SessionID: id, SessionTitle: "", ProjectName: "‮", Kind: KindWaiting},
			Message{Title: "A session is waiting for you", Body: "tap to answer", SessionID: id, Kind: KindWaiting}},
	} {
		got, ok := eventMessage(test.event)
		if !ok || got != test.want {
			t.Errorf("eventMessage(%+v) = %+v, %v", test.event, got, ok)
		}
	}
	long, _ := eventMessage(Event{SessionID: id, SessionTitle: strings.Repeat("é", 500), Kind: KindWaiting})
	if n := len([]rune(long.Title)); n > maxLabelRunes+30 {
		t.Fatalf("title has %d runes", n)
	}
	for _, event := range []Event{{SessionID: "x", Kind: KindWaiting}, {SessionID: id, Kind: "other"}} {
		if _, ok := eventMessage(event); ok {
			t.Errorf("%+v accepted", event)
		}
	}
}

// pushService is a fake transport: no network, and it records every request.
type pushService struct {
	mu       sync.Mutex
	status   map[string]int
	requests []*http.Request
	bodies   map[string][]byte
	seen     chan string
}

func (service *pushService) RoundTrip(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	service.mu.Lock()
	service.requests = append(service.requests, request)
	service.bodies[request.URL.String()] = body
	status := service.status[request.URL.String()]
	service.mu.Unlock()
	if status == 0 {
		status = http.StatusCreated
	}
	service.seen <- request.URL.String()
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

type testPhone struct {
	endpoint string
	key      *ecdh.PrivateKey
	auth     []byte
}

func newPhone(t *testing.T, endpoint string) testPhone {
	key, err := ecdh.P256().GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return testPhone{endpoint: endpoint, key: key, auth: bytes.Repeat([]byte{7}, 16)}
}

func (phone testPhone) subscription() Subscription {
	return Subscription{Endpoint: phone.endpoint, P256dh: phone.key.PublicKey().Bytes(), Auth: phone.auth}
}

func pairDevice(t *testing.T, authority *auth.Service) auth.PollResult {
	t.Helper()
	ctx := context.Background()
	grant, _, err := authority.CreateGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := authority.Redeem(ctx, grant, "Phone")
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Approve(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	result, err := authority.Poll(ctx, redeemed.PendingID, redeemed.PollSecret)
	if err != nil || !result.Approved {
		t.Fatalf("poll = %v", err)
	}
	return result
}

func TestNotifierFanOutCoalescingAndRevocation(t *testing.T) {
	ctx := context.Background()
	persistence, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	authority, err := auth.NewService(auth.ServiceConfig{Store: persistence})
	if err != nil {
		t.Fatal(err)
	}
	service := &pushService{status: map[string]int{}, bodies: map[string][]byte{}, seen: make(chan string, 64)}
	config := NotifierConfig{Store: persistence, Revoked: authority.DeviceRevoked,
		CredentialIdle: auth.BrowserInactivityLifetime, Client: &http.Client{Transport: service}}
	notifier, err := NewNotifier(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewNotifier(ctx, config)
	if err != nil || again.PublicKey() != notifier.PublicKey() || len(decode(t, notifier.PublicKey())) != 65 {
		t.Fatal("VAPID key was not persisted")
	}

	alpha, bravo := pairDevice(t, authority), pairDevice(t, authority)
	alphaPhone := newPhone(t, "https://fcm.googleapis.com/fcm/send/alpha")
	bravoPhone := newPhone(t, "https://web.push.apple.com/bravo")
	if err := notifier.Subscribe(ctx, alpha.DeviceID, alphaPhone.subscription()); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Subscribe(ctx, bravo.DeviceID, bravoPhone.subscription()); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Subscription{
		{Endpoint: "https://evil.example/x", P256dh: alphaPhone.subscription().P256dh, Auth: alphaPhone.auth},
		{Endpoint: "https://fcm.googleapis.com/x", P256dh: make([]byte, 65), Auth: alphaPhone.auth},
		{Endpoint: "https://fcm.googleapis.com/x", P256dh: alphaPhone.subscription().P256dh, Auth: []byte{1}},
	} {
		if err := notifier.Subscribe(ctx, alpha.DeviceID, bad); err == nil {
			t.Errorf("subscription %q accepted", bad.Endpoint)
		}
	}

	// The test path reaches only the calling device.
	if sent, err := notifier.Test(ctx, alpha.DeviceID); err != nil || sent != 1 || <-service.seen != alphaPhone.endpoint {
		t.Fatalf("test sent %d, %v", sent, err)
	}
	if _, err := notifier.Test(ctx, alpha.DeviceID); err != ErrRateLimited {
		t.Fatalf("second test within cooldown = %v", err)
	}
	request := service.requests[0]
	if request.Method != http.MethodPost || request.Header.Get("Content-Encoding") != "aes128gcm" ||
		request.Header.Get("TTL") != "60" || request.Header.Get("Urgency") != "normal" ||
		!vapidHeader.MatchString(request.Header.Get("Authorization")) {
		t.Fatalf("headers = %v", request.Header)
	}

	sessionID := "11111111-2222-4333-8444-555555555555"
	event := Event{SessionID: sessionID, SessionTitle: "claude", ProjectName: "api", Kind: KindWaiting}
	if !notifier.Notify(ctx, event) {
		t.Fatal("notify dropped")
	}
	got := map[string]bool{<-service.seen: true, <-service.seen: true}
	if !got[alphaPhone.endpoint] || !got[bravoPhone.endpoint] {
		t.Fatalf("delivered to %v", got)
	}
	service.mu.Lock()
	plain := decryptForTest(t, service.bodies[alphaPhone.endpoint], alphaPhone.key, alphaPhone.auth)
	urgency := service.requests[len(service.requests)-1].Header.Get("Urgency")
	service.mu.Unlock()
	var message Message
	if err := json.Unmarshal(plain, &message); err != nil || message != (Message{
		Title: "claude is waiting for you", Body: "api · tap to answer", SessionID: sessionID, Kind: KindWaiting,
	}) || urgency != "high" {
		t.Fatalf("message = %s, urgency %s", plain, urgency)
	}
	if notifier.Notify(ctx, event) {
		t.Fatal("same session and kind within the window was not coalesced")
	}

	// A gone subscription is deleted.
	service.mu.Lock()
	service.status[bravoPhone.endpoint] = http.StatusGone
	service.mu.Unlock()
	if !notifier.Notify(ctx, Event{SessionID: sessionID, SessionTitle: "claude", Kind: KindDone, Duration: time.Hour}) {
		t.Fatal("done event dropped")
	}
	<-service.seen
	<-service.seen
	waitFor(t, func() bool {
		left, _ := persistence.ListPushSubscriptions(ctx, bravo.DeviceID, time.Now(), auth.BrowserInactivityLifetime, 10)
		return len(left) == 0
	})

	// Revoking a device deletes its subscriptions; logout does the same.
	if _, err := authority.RevokeDevice(ctx, alpha.DeviceID); err != nil {
		t.Fatal(err)
	}
	if err := notifier.Subscribe(ctx, alpha.DeviceID, alphaPhone.subscription()); err == nil {
		t.Fatal("revoked device subscribed")
	}
	if left, _ := persistence.ListPushSubscriptions(ctx, "", time.Now(), auth.BrowserInactivityLifetime, 10); len(left) != 0 {
		t.Fatalf("subscriptions after revoke = %d", len(left))
	}
	charlie := pairDevice(t, authority)
	if err := notifier.Subscribe(ctx, charlie.DeviceID, bravoPhone.subscription()); err != nil {
		t.Fatal(err)
	}
	if err := authority.Logout(ctx, charlie.Credential); err != nil {
		t.Fatal(err)
	}
	if left, _ := persistence.ListPushSubscriptions(ctx, "", time.Now(), auth.BrowserInactivityLifetime, 10); len(left) != 0 {
		t.Fatalf("subscriptions after logout = %d", len(left))
	}
}

func TestSubscriptionCaps(t *testing.T) {
	ctx := context.Background()
	persistence, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistence.Close() })
	authority, _ := auth.NewService(auth.ServiceConfig{Store: persistence})
	notifier, err := NewNotifier(ctx, NotifierConfig{Store: persistence, Revoked: authority.DeviceRevoked,
		CredentialIdle: auth.BrowserInactivityLifetime})
	if err != nil {
		t.Fatal(err)
	}
	device := pairDevice(t, authority)
	for index := range MaxDeviceSubscriptions + 3 {
		phone := newPhone(t, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", index))
		if err := notifier.Subscribe(ctx, device.DeviceID, phone.subscription()); err != nil {
			t.Fatal(err)
		}
	}
	left, _ := persistence.ListPushSubscriptions(ctx, device.DeviceID, time.Now(), auth.BrowserInactivityLifetime, 100)
	if len(left) != MaxDeviceSubscriptions {
		t.Fatalf("device keeps %d subscriptions", len(left))
	}
	if err := notifier.Unsubscribe(ctx, device.DeviceID, left[0].Endpoint); err != nil {
		t.Fatal(err)
	}
	left, _ = persistence.ListPushSubscriptions(ctx, device.DeviceID, time.Now(), auth.BrowserInactivityLifetime, 100)
	if len(left) != MaxDeviceSubscriptions-1 {
		t.Fatalf("after unsubscribe %d", len(left))
	}
}

func TestGlobalRateLimit(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	notifier := &Notifier{config: NotifierConfig{Now: func() time.Time { return now }}, last: map[string]time.Time{}}
	for index := range MaxPerMinute {
		if !notifier.admit(fmt.Sprint(index), CoalesceWindow) {
			t.Fatalf("admit %d refused", index)
		}
	}
	if notifier.admit("another", CoalesceWindow) {
		t.Fatal("per-minute cap not enforced")
	}
	now = now.Add(time.Minute)
	if !notifier.admit("another", CoalesceWindow) || !notifier.admit("0", CoalesceWindow) {
		t.Fatal("window did not reset")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
