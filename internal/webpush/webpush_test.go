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
	"sync/atomic"
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
	grant, _, err := authority.CreateBoundGrant(ctx, store.AppBinding{})
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := authority.RedeemChecked(ctx, grant, "Phone", auth.OwnOriginOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.Approve(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	result, err := authority.PollChecked(ctx, redeemed.PendingID, redeemed.PollSecret, auth.OwnOriginOnly)
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
		if !notifier.admit(fmt.Sprint(index), CoalesceWindow, &notifier.sessions, MaxPerMinute) {
			t.Fatalf("admit %d refused", index)
		}
	}
	if notifier.admit("another", CoalesceWindow, &notifier.sessions, MaxPerMinute) {
		t.Fatal("per-minute cap not enforced")
	}
	now = now.Add(time.Minute)
	if !notifier.admit("another", CoalesceWindow, &notifier.sessions, MaxPerMinute) || !notifier.admit("0", CoalesceWindow, &notifier.sessions, MaxPerMinute) {
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

func TestProviderMessagesUseTheFixedFormat(t *testing.T) {
	target := "w8:p1.term_65d0031602b777"
	for _, test := range []struct {
		event ProviderEvent
		want  Message
	}{
		{ProviderEvent{Provider: "herdr", Target: target, Group: "shop", Item: "silver-meadow", Agent: "claude", Kind: KindWaiting},
			Message{Title: "herdr · shop · silver-meadow", Body: "claude needs you. Tap to answer.", Kind: KindWaiting, Provider: "herdr", Target: target}},
		{ProviderEvent{Provider: "herdr", Target: target, Group: "shop", Item: "lucky-valley", Agent: "claude", Kind: KindDone, Duration: 12*time.Minute + 50*time.Second},
			Message{Title: "herdr · shop · lucky-valley", Body: "claude finished after 12 min.", Kind: KindDone, Provider: "herdr", Target: target}},
		{ProviderEvent{Provider: "herdr", Target: target, Group: "notes", Item: "notes", Agent: "codex", Kind: KindDone, Duration: 40 * time.Second},
			Message{Title: "herdr · notes", Body: "codex finished.", Kind: KindDone, Provider: "herdr", Target: target}},
		{ProviderEvent{Provider: "herdr", Target: target, Group: "sh\u202eop\u200b", Item: " a\x1b[2Jb ", Kind: KindWaiting},
			Message{Title: "herdr · sh op · a [2Jb", Body: "The agent needs you. Tap to answer.", Kind: KindWaiting, Provider: "herdr", Target: target}},
	} {
		got, ok := ProviderMessage(test.event)
		if !ok || got != test.want {
			t.Errorf("ProviderMessage(%+v) = %+v, %v", test.event, got, ok)
		}
	}
	long, _ := ProviderMessage(ProviderEvent{Provider: "herdr", Target: target, Group: strings.Repeat("g", 100), Item: strings.Repeat("i", 100), Kind: KindWaiting})
	if long.Title != "herdr · "+strings.Repeat("g", 39)+"… · "+strings.Repeat("i", 39)+"…" {
		t.Fatalf("names are not cut to 40: %q", long.Title)
	}
	for _, event := range []ProviderEvent{
		{Provider: "herdr", Target: "../x", Kind: KindWaiting},
		{Provider: "Herdr", Target: target, Kind: KindWaiting},
		{Provider: "herdr", Target: target, Kind: "other"},
	} {
		if _, ok := ProviderMessage(event); ok {
			t.Errorf("%+v accepted", event)
		}
	}
	// A provider message carries no session ID, a session message no target.
	data, _ := json.Marshal(test0(target))
	if strings.Contains(string(data), "sessionId") {
		t.Fatalf("provider payload %s", data)
	}
}

func test0(target string) Message {
	message, _ := ProviderMessage(ProviderEvent{Provider: "herdr", Target: target, Group: "a", Item: "b", Kind: KindWaiting})
	return message
}

// Provider pushes have their own per-minute budget: a flood of herdr changes
// never takes a session push's budget, and the reverse.
func TestProviderBudgetIsSeparate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	notifier := &Notifier{config: NotifierConfig{Now: func() time.Time { return now }}, last: map[string]time.Time{}}
	for index := range ProviderMaxPerMinute {
		if !notifier.admit(fmt.Sprintf("provider:t%d:waiting", index), CoalesceWindow, &notifier.provider, ProviderMaxPerMinute) {
			t.Fatalf("provider admit %d refused", index)
		}
	}
	if notifier.admit("provider:another:waiting", CoalesceWindow, &notifier.provider, ProviderMaxPerMinute) {
		t.Fatal("provider cap not enforced")
	}
	for index := range MaxPerMinute {
		if !notifier.admit(fmt.Sprint(index), CoalesceWindow, &notifier.sessions, MaxPerMinute) {
			t.Fatalf("session admit %d refused after a provider flood", index)
		}
	}
	// The same target and kind within a minute is coalesced.
	now = now.Add(time.Minute)
	if !notifier.admit("provider:t1:done", CoalesceWindow, &notifier.provider, ProviderMaxPerMinute) ||
		notifier.admit("provider:t1:done", CoalesceWindow, &notifier.provider, ProviderMaxPerMinute) {
		t.Fatal("provider coalescing")
	}
}

// Two agents can change in one poll: the second push must go out while the
// first is still on its way.
func TestSecondProviderPushIsNotDroppedWhileTheFirstIsInFlight(t *testing.T) {
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
	notifier, err := NewNotifier(ctx, NotifierConfig{Store: persistence, Revoked: authority.DeviceRevoked, CredentialIdle: auth.BrowserInactivityLifetime})
	if err != nil {
		t.Fatal(err)
	}
	notifier.providerInFlight <- struct{}{} // a delivery in flight
	event := ProviderEvent{Provider: "herdr", Target: "w2:p1.term_b", Group: "shop", Item: "lucky-valley", Agent: "claude", Kind: KindWaiting}
	if !notifier.NotifyProvider(ctx, event) {
		t.Fatal("the second provider push was dropped")
	}
}

// slowService is a push service that holds every request until release closes.
type slowService struct {
	release chan struct{}
	started chan struct{}
}

func (service slowService) RoundTrip(request *http.Request) (*http.Response, error) {
	service.started <- struct{}{}
	select {
	case <-service.release:
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
	return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

// Issue #197: several sessions can turn waiting together while the push
// service is slow. Each admitted push must go out, up to the per-minute cap.
func TestSessionPushIsNotDroppedWhileEarlierOnesAreInFlight(t *testing.T) {
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
	service := slowService{release: make(chan struct{}), started: make(chan struct{}, MaxPerMinute)}
	// A clock the test can move: deliveries read it from their own goroutines.
	var later atomic.Int64
	clock := func() time.Time { return time.Now().Add(time.Duration(later.Load())) }
	notifier, err := NewNotifier(ctx, NotifierConfig{Store: persistence, Revoked: authority.DeviceRevoked,
		CredentialIdle: auth.BrowserInactivityLifetime, Client: &http.Client{Transport: service}, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	if err := notifier.Subscribe(ctx, pairDevice(t, authority).DeviceID, newPhone(t, "https://fcm.googleapis.com/fcm/send/slow").subscription()); err != nil {
		t.Fatal(err)
	}
	for index := range MaxPerMinute {
		event := Event{SessionID: fmt.Sprintf("11111111-2222-4333-8444-%012d", index), SessionTitle: "claude", Kind: KindWaiting}
		if !notifier.Notify(ctx, event) {
			t.Fatalf("session push %d was dropped while %d were in flight", index+1, index)
		}
	}
	// The per-minute cap still holds.
	if notifier.Notify(ctx, Event{SessionID: "11111111-2222-4333-8444-999999999999", Kind: KindWaiting}) {
		t.Fatal("a push over the per-minute cap was accepted")
	}
	// Every admitted push reaches the push service, and the slots come back.
	for range MaxPerMinute {
		select {
		case <-service.started:
		case <-time.After(5 * time.Second):
			t.Fatal("an admitted push never reached the push service")
		}
	}
	// Two windows can be alive together: a minute later the budget is new while these 12 are still
	// on their way (a delivery may take a minute). The next 12 must go out too, and the 25th not.
	later.Store(int64(time.Minute))
	for index := range MaxPerMinute {
		event := Event{SessionID: fmt.Sprintf("22222222-2222-4333-8444-%012d", index), SessionTitle: "claude", Kind: KindWaiting}
		if !notifier.Notify(ctx, event) {
			t.Fatalf("push %d of the next minute was dropped while %d were in flight", index+1, MaxPerMinute+index)
		}
	}
	if notifier.Notify(ctx, Event{SessionID: "22222222-2222-4333-8444-999999999999", Kind: KindWaiting}) {
		t.Fatal("a 25th push was accepted")
	}
	close(service.release)
	waitFor(t, func() bool { return len(notifier.inFlight) == 0 })
}

// newTestNotifier is a notifier on a fresh store. Its clock stands still
// until the test moves later, so subscriptions made together are listed by
// endpoint (ListPushSubscriptions orders by time, then endpoint).
func newTestNotifier(t *testing.T, transport http.RoundTripper, later *atomic.Int64) (*Notifier, *auth.Service) {
	t.Helper()
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
	// Ahead of the real clock: a device is live only when it was paired before "now".
	start := time.Now().Add(time.Minute)
	notifier, err := NewNotifier(ctx, NotifierConfig{Store: persistence, Revoked: authority.DeviceRevoked,
		CredentialIdle: auth.BrowserInactivityLifetime, Client: &http.Client{Transport: transport},
		Now: func() time.Time { return start.Add(time.Duration(later.Load())) }})
	if err != nil {
		t.Fatal(err)
	}
	return notifier, authority
}

// subscribeAll subscribes each endpoint, MaxDeviceSubscriptions to a device.
func subscribeAll(t *testing.T, notifier *Notifier, authority *auth.Service, endpoints ...string) {
	t.Helper()
	device := ""
	for index, endpoint := range endpoints {
		if index%MaxDeviceSubscriptions == 0 {
			device = pairDevice(t, authority).DeviceID
		}
		if err := notifier.Subscribe(context.Background(), device, newPhone(t, endpoint).subscription()); err != nil {
			t.Fatal(err)
		}
	}
}

// Issue #202: a phone that taps "Send a test notification" every 5 s for a
// minute must not use up the budget of the session pushes.
func TestTestSendsDoNotUseTheSessionBudget(t *testing.T) {
	ctx := context.Background()
	var later atomic.Int64
	notifier, _ := newTestNotifier(t, nil, &later)
	for tap := range int(time.Minute / testCooldown) {
		later.Store(int64(time.Duration(tap) * testCooldown))
		// The test budget holds: the first TestMaxPerMinute go out, the rest are refused.
		// A new device for each tap: the budget is one for all devices (the 5 s cooldown is per device).
		if _, err := notifier.Test(ctx, fmt.Sprintf("device-%d", tap)); (err == nil) != (tap < TestMaxPerMinute) || (err != nil && err != ErrRateLimited) {
			t.Fatalf("test send %d = %v", tap+1, err)
		}
	}
	for index := range MaxPerMinute {
		event := Event{SessionID: fmt.Sprintf("11111111-2222-4333-8444-%012d", index), SessionTitle: "claude", Kind: KindWaiting}
		if !notifier.Notify(ctx, event) {
			t.Fatalf("session push %d was refused after a minute of test sends", index+1)
		}
	}
	waitFor(t, func() bool { return len(notifier.inFlight) == 0 })
}

// hungService is a push service where an endpoint with "hung" in its address
// holds the request until release closes or the request's context ends. The
// other endpoints answer at once.
type hungService struct {
	release      chan struct{}
	started      chan string
	active, most atomic.Int32
}

func (service *hungService) RoundTrip(request *http.Request) (*http.Response, error) {
	now := service.active.Add(1)
	defer service.active.Add(-1)
	for most := service.most.Load(); now > most && !service.most.CompareAndSwap(most, now); most = service.most.Load() {
	}
	service.started <- request.URL.String()
	if strings.Contains(request.URL.Path, "hung") {
		select {
		case <-service.release:
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	}
	return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

// Issue #202: push endpoints that hang must not hold back a later phone.
func TestHungEndpointsDoNotStarveALaterPhone(t *testing.T) {
	service := &hungService{release: make(chan struct{}), started: make(chan string, MaxSubscriptions)}
	notifier, authority := newTestNotifier(t, service, new(atomic.Int64))
	healthy := "https://fcm.googleapis.com/fcm/send/z-healthy"
	// One fewer than deliverFanOut hang, and they are listed first.
	subscribeAll(t, notifier, authority, "https://fcm.googleapis.com/fcm/send/a-hung-0",
		"https://fcm.googleapis.com/fcm/send/a-hung-1", "https://fcm.googleapis.com/fcm/send/a-hung-2", healthy)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		accepted, _ := notifier.deliver(ctx, "", Message{Title: "t", Kind: KindWaiting}, time.Minute, UrgencyHigh)
		done <- accepted
	}()
	defer func() { cancel(); <-done }()
	for reached := false; !reached; {
		select {
		case endpoint := <-service.started:
			reached = endpoint == healthy
		case <-time.After(5 * time.Second):
			t.Fatal("the later phone got no push while earlier endpoints hang")
		}
	}
	close(service.release)
	if accepted := <-done; accepted != 4 {
		t.Fatalf("accepted = %d, want 4", accepted)
	}
	done <- 0 // for the deferred read
}

// The bounds of one delivery: at most deliverFanOut requests at once, none
// started after the deadline, and none alive when deliver returns.
func TestDeliveryFanOutIsBoundedAndEndsAtTheDeadline(t *testing.T) {
	service := &hungService{release: make(chan struct{}), started: make(chan string, MaxSubscriptions)}
	notifier, authority := newTestNotifier(t, service, new(atomic.Int64))
	var endpoints []string
	for index := range 2 * deliverFanOut {
		endpoints = append(endpoints, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/a-hung-%d", index))
	}
	subscribeAll(t, notifier, authority, append(endpoints, "https://fcm.googleapis.com/fcm/send/z-healthy")...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		accepted, _ := notifier.deliver(ctx, "", Message{Title: "t", Kind: KindWaiting}, time.Minute, UrgencyHigh)
		done <- accepted
	}()
	for range deliverFanOut {
		select {
		case <-service.started:
		case <-time.After(5 * time.Second):
			t.Fatal("the delivery did not open deliverFanOut requests")
		}
	}
	cancel() // the deadline
	if accepted := <-done; accepted != 0 || service.active.Load() != 0 || service.most.Load() != deliverFanOut || len(service.started) != 0 {
		t.Fatalf("accepted %d, %d requests alive after deliver, at most %d at once, %d started after the deadline",
			accepted, service.active.Load(), service.most.Load(), len(service.started))
	}
}
