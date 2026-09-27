package webpush

import (
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/intojhanurag/phone-terminal/internal/store"
)

const (
	// MaxDeviceSubscriptions and MaxSubscriptions bound rows a browser can create.
	MaxDeviceSubscriptions = 5
	MaxSubscriptions       = 64

	// CoalesceWindow allows one notification per session and kind.
	CoalesceWindow = time.Minute
	// MaxPerMinute caps all notifications, tests included.
	MaxPerMinute = 12

	testCooldown   = 5 * time.Second
	deliverTimeout = time.Minute
	maxInFlight    = 2
	maxLabelRunes  = 60
	maxTracked     = 1024

	// Subject is the VAPID contact claim. It names the project only, so no
	// tailnet host name or user identity reaches a push service.
	Subject = "https://github.com/intojhanurag/phone-terminal"
)

// Kind is the agent state a notification reports.
type Kind string

const (
	KindWaiting Kind = "waiting"
	KindDone    Kind = "done"
	kindTest    Kind = "test"
)

// Event is what the agent-state detector reports. It must never carry
// terminal text: only the session's own title, its project name, and how
// long the agent worked.
type Event struct {
	SessionID    string
	SessionTitle string
	ProjectName  string
	Kind         Kind
	Duration     time.Duration
}

// Message is the JSON the service worker receives after decryption.
type Message struct {
	Title     string `json:"title"`
	Body      string `json:"body"`
	SessionID string `json:"sessionId,omitempty"`
	Kind      Kind   `json:"kind"`
}

// NotifierConfig wires the broker-owned store. Revoked is the auth
// service's in-memory revocation check; Client defaults to NewHTTPClient.
type NotifierConfig struct {
	Store          *store.Store
	Revoked        func(deviceID string) bool
	CredentialIdle time.Duration
	Client         *http.Client
	Now            func() time.Time
}

// Notifier owns the VAPID keys and fans notifications out to subscribed,
// still-authorized devices. It runs in the broker, which owns devices,
// sessions and revocation.
type Notifier struct {
	config   NotifierConfig
	keys     Keys
	inFlight chan struct{}

	mu     sync.Mutex
	last   map[string]time.Time // coalescing key -> last send
	window time.Time
	sent   int
}

// NewNotifier loads the VAPID key from the store, creating it on first use.
func NewNotifier(ctx context.Context, config NotifierConfig) (*Notifier, error) {
	if config.Store == nil || config.Revoked == nil || config.CredentialIdle <= 0 {
		return nil, errors.New("webpush: notifier needs a store, a revocation check and an idle lifetime")
	}
	if config.Client == nil {
		config.Client = NewHTTPClient()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	candidate, err := GenerateKeys()
	if err != nil {
		return nil, err
	}
	raw, err := candidate.PrivateBytes()
	if err != nil {
		return nil, err
	}
	saved, err := config.Store.LoadOrCreateVAPIDKey(ctx, raw, config.Now())
	if err != nil {
		return nil, err
	}
	keys, err := ParseKeys(saved)
	if err != nil {
		return nil, err
	}
	return &Notifier{config: config, keys: keys, inFlight: make(chan struct{}, maxInFlight), last: map[string]time.Time{}}, nil
}

// PublicKey is the applicationServerKey for PushManager.subscribe.
func (notifier *Notifier) PublicKey() string { return notifier.keys.PublicKey() }

// Subscribe saves a subscription for the authenticated device.
func (notifier *Notifier) Subscribe(ctx context.Context, deviceID string, subscription Subscription) error {
	if err := ValidateEndpoint(subscription.Endpoint); err != nil {
		return err
	}
	if _, err := ecdh.P256().NewPublicKey(subscription.P256dh); err != nil || len(subscription.Auth) != AuthSecretBytes {
		return ErrKey
	}
	return notifier.config.Store.SavePushSubscription(ctx, store.PushSubscription{
		DeviceID: deviceID, Endpoint: subscription.Endpoint, P256dh: subscription.P256dh, Auth: subscription.Auth,
	}, MaxDeviceSubscriptions, MaxSubscriptions, notifier.config.Now())
}

// Unsubscribe removes one of the authenticated device's subscriptions.
func (notifier *Notifier) Unsubscribe(ctx context.Context, deviceID, endpoint string) error {
	return notifier.config.Store.DeletePushSubscription(ctx, deviceID, endpoint)
}

// Notify is the hook for the agent-state detector. It returns at once:
// delivery runs in the background, detached from ctx cancellation. It
// reports false when the event was dropped: invalid, coalesced (the same
// session and kind within CoalesceWindow), over MaxPerMinute, or while
// earlier deliveries are still in flight.
func (notifier *Notifier) Notify(ctx context.Context, event Event) bool {
	message, ok := eventMessage(event)
	if !ok || !notifier.admit(string(event.Kind)+":"+event.SessionID, CoalesceWindow) {
		return false
	}
	select {
	case notifier.inFlight <- struct{}{}:
	default:
		return false
	}
	urgency, ttl := UrgencyNormal, 2*time.Minute
	if event.Kind == KindWaiting {
		urgency, ttl = UrgencyHigh, 5*time.Minute
	}
	go func() {
		defer func() { <-notifier.inFlight }()
		deliverCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverTimeout)
		defer cancel()
		_, _ = notifier.deliver(deliverCtx, "", message, ttl, urgency)
	}()
	return true
}

// ErrRateLimited reports a dropped test notification.
var ErrRateLimited = errors.New("webpush: notification rate limit reached")

// Test sends a fixed test notification to one device's subscriptions and
// returns how many push services accepted it.
func (notifier *Notifier) Test(ctx context.Context, deviceID string) (int, error) {
	if !notifier.admit("test:"+deviceID, testCooldown) {
		return 0, ErrRateLimited
	}
	return notifier.deliver(ctx, deviceID, Message{
		Title: "Phone Terminal", Body: "Notifications are on.", Kind: kindTest,
	}, time.Minute, UrgencyNormal)
}

// admit applies coalescing for key and the global per-minute cap.
func (notifier *Notifier) admit(key string, window time.Duration) bool {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	now := notifier.config.Now()
	if last, ok := notifier.last[key]; ok && now.Sub(last) < window && !now.Before(last) {
		return false
	}
	if now.Before(notifier.window) || now.Sub(notifier.window) >= time.Minute {
		notifier.window, notifier.sent = now, 0
	}
	if notifier.sent >= MaxPerMinute {
		return false
	}
	notifier.sent++
	if len(notifier.last) >= maxTracked {
		for other, when := range notifier.last {
			if now.Sub(when) >= CoalesceWindow {
				delete(notifier.last, other)
			}
		}
		if len(notifier.last) >= maxTracked {
			clear(notifier.last) // ponytail: forgets coalescing under a flood; the per-minute cap still holds.
		}
	}
	notifier.last[key] = now
	return true
}

func (notifier *Notifier) deliver(ctx context.Context, deviceID string, message Message, ttl time.Duration, urgency string) (int, error) {
	payload, err := json.Marshal(message)
	if err != nil {
		return 0, err
	}
	subscriptions, err := notifier.config.Store.ListPushSubscriptions(ctx, deviceID, notifier.config.Now(),
		notifier.config.CredentialIdle, MaxSubscriptions)
	if err != nil {
		return 0, err
	}
	accepted := 0
	// ponytail: sequential fan-out under one deadline; fine for a handful of phones.
	for _, subscription := range subscriptions {
		if notifier.config.Revoked(subscription.DeviceID) {
			continue
		}
		status, err := Send(ctx, notifier.config.Client, notifier.keys, Subscription{
			Endpoint: subscription.Endpoint, P256dh: subscription.P256dh, Auth: subscription.Auth,
		}, payload, Subject, ttl, urgency)
		switch {
		case err == nil && status >= 200 && status < 300:
			accepted++
		case err == nil && (status == http.StatusNotFound || status == http.StatusGone),
			errors.Is(err, ErrEndpoint), errors.Is(err, ErrKey):
			// The subscription expired or can never work: forget it.
			_ = notifier.config.Store.DeletePushSubscription(ctx, "", subscription.Endpoint)
		}
	}
	return accepted, nil
}

// eventMessage builds the lock-screen text from names only. Terminal text
// never enters an Event, and names are cleaned and shortened here.
func eventMessage(event Event) (Message, bool) {
	if !validUUID(event.SessionID) {
		return Message{}, false
	}
	title := cleanLabel(event.SessionTitle, "A session")
	project := cleanLabel(event.ProjectName, "")
	message := Message{SessionID: event.SessionID, Kind: event.Kind}
	switch event.Kind {
	case KindWaiting:
		message.Title = title + " is waiting for you"
		message.Body = "tap to answer"
		if project != "" {
			message.Body = project + " · tap to answer"
		}
	case KindDone:
		message.Title = title + " is done · your turn"
		message.Body = fmt.Sprintf("Worked for %d min", max(1, int(event.Duration.Round(time.Minute)/time.Minute)))
	default:
		return Message{}, false
	}
	return message, true
}

// cleanLabel drops control and format characters, collapses spaces, and
// keeps at most maxLabelRunes runes.
func cleanLabel(value, fallback string) string {
	var builder strings.Builder
	runes := 0
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == unicode.ReplacementChar
	}) {
		for _, character := range field {
			if runes == maxLabelRunes {
				return strings.TrimSpace(builder.String()) + "…"
			}
			builder.WriteRune(character)
			runes++
		}
		if runes < maxLabelRunes {
			builder.WriteByte(' ')
			runes++
		}
	}
	if result := strings.TrimSpace(builder.String()); result != "" {
		return result
	}
	return fallback
}

func validUUID(value string) bool {
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
