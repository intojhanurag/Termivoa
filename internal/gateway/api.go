package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	productionCookieName  = "__Host-pterm_session"
	developmentCookieName = "pterm_dev_session"
	maxRequestBodyBytes   = 64 * 1024
	requestHeaderName     = "X-Pterm-Request"
	maxDeviceLabelBytes   = 64
	maxSessionTitleBytes  = 128
	maxErrorMessageBytes  = 512
)

func (handler *gatewayHandler) pairRedeem(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodPost) {
		return
	}
	var body protocol.HTTPPairRedeemRequest
	if !handler.mutation(response, request, &body) {
		return
	}
	if !handler.redeemLimit.take(clientKey(request)) {
		writeError(response, http.StatusTooManyRequests, protocol.LimitExceeded, "too many pairing attempts")
		return
	}
	if !validSecret(body.Grant) || !validText(body.DeviceLabel, maxDeviceLabelBytes) {
		writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "invalid pairing request")
		return
	}
	result, err := handler.api.RedeemPairing(request.Context(), body)
	if err != nil {
		writeBrokerError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (handler *gatewayHandler) pairPoll(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodPost) {
		return
	}
	var body protocol.HTTPPairPollRequest
	if !handler.mutation(response, request, &body) {
		return
	}
	if !handler.pollLimit.take(clientKey(request)) {
		writeError(response, http.StatusTooManyRequests, protocol.LimitExceeded, "too many pairing polls")
		return
	}
	if !validUUID(body.PendingID) || !validSecret(body.PollSecret) {
		writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "invalid poll request")
		return
	}
	result, err := handler.api.PollPairing(request.Context(), body)
	if err != nil {
		writeBrokerError(response, err)
		return
	}
	// The broker returns the credential exactly once, on the approved
	// response. Anything else is a contract violation and sets no cookie.
	switch {
	case result.Response.Status == protocol.Pending && result.Credential == "":
	case result.Response.Status == protocol.Approved && validSecret(result.Credential) &&
		time.Until(result.CredentialExpiresAt) > 0:
		handler.setCookie(response, result.Credential, result.CredentialExpiresAt)
	default:
		writeError(response, http.StatusInternalServerError, protocol.InternalError, "internal error")
		return
	}
	writeJSON(response, http.StatusOK, result.Response)
}

func (handler *gatewayHandler) settings(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet) {
		return
	}
	credential, ok := handler.credential(response, request)
	if !ok {
		return
	}
	result, err := handler.api.Settings(request.Context(), credential)
	handler.respond(response, request, result, err)
}

func (handler *gatewayHandler) sessions(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet, http.MethodPost) {
		return
	}
	if request.Method == http.MethodGet {
		credential, ok := handler.credential(response, request)
		if !ok {
			return
		}
		result, err := handler.api.ListSessions(request.Context(), credential)
		handler.respond(response, request, result, err)
		return
	}
	var body protocol.HTTPSessionCreateRequest
	if !handler.mutation(response, request, &body) {
		return
	}
	credential, ok := handler.credential(response, request)
	if !ok {
		return
	}
	if !validUUID(body.OperationID) || !validUUID(body.ProjectID) || !validUUID(body.ShellProfileID) {
		writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "invalid session request")
		return
	}
	result, err := handler.api.CreateSession(request.Context(), credential, body)
	handler.respond(response, request, result, err)
}

func (handler *gatewayHandler) renameSession(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodPatch) {
		return
	}
	var body protocol.HTTPSessionRenameRequest
	if !handler.mutation(response, request, &body) {
		return
	}
	credential, ok := handler.credential(response, request)
	if !ok {
		return
	}
	sessionID := request.PathValue("id")
	if !validUUID(sessionID) || !validUUID(body.OperationID) || !validUUID(body.Generation) ||
		!validText(body.Title, maxSessionTitleBytes) {
		writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "invalid rename request")
		return
	}
	result, err := handler.api.RenameSession(request.Context(), credential, sessionID, body)
	handler.respond(response, request, result, err)
}

func (handler *gatewayHandler) terminateSession(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodPost) {
		return
	}
	var body protocol.HTTPSessionTerminateRequest
	if !handler.mutation(response, request, &body) {
		return
	}
	credential, ok := handler.credential(response, request)
	if !ok {
		return
	}
	sessionID := request.PathValue("id")
	if !validUUID(sessionID) || !validUUID(body.OperationID) || !validUUID(body.Generation) {
		writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "invalid terminate request")
		return
	}
	result, err := handler.api.TerminateSession(request.Context(), credential, sessionID, body)
	handler.respond(response, request, result, err)
}

// logout always clears the cookie once the broker has no valid credential
// for it. On other broker failures the cookie stays so the user can retry.
func (handler *gatewayHandler) logout(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodPost) {
		return
	}
	var body struct{}
	if !handler.mutation(response, request, &body) {
		return
	}
	if cookie, err := request.Cookie(handler.cookieName()); err == nil && validSecret(cookie.Value) {
		if err := handler.api.Logout(request.Context(), cookie.Value); err != nil && !isAuthFailure(err) {
			writeBrokerError(response, err)
			return
		}
	}
	handler.clearCookie(response)
	response.WriteHeader(http.StatusNoContent)
}

// respond writes a broker result. Authentication failures are counted
// against the client and clear the stale cookie.
func (handler *gatewayHandler) respond(response http.ResponseWriter, request *http.Request, result any, err error) {
	if err == nil {
		writeJSON(response, http.StatusOK, result)
		return
	}
	if isAuthFailure(err) {
		handler.clearCookie(response)
		if !handler.authFailLimit.take(clientKey(request)) {
			writeError(response, http.StatusTooManyRequests, protocol.LimitExceeded, "too many failed attempts")
			return
		}
	}
	writeBrokerError(response, err)
}

// credential returns the raw session cookie. The broker authorizes it; the
// gateway only rejects obviously malformed values and throttled clients.
func (handler *gatewayHandler) credential(response http.ResponseWriter, request *http.Request) (string, bool) {
	key := clientKey(request)
	if !handler.authFailLimit.allowed(key) {
		writeError(response, http.StatusTooManyRequests, protocol.LimitExceeded, "too many failed attempts")
		return "", false
	}
	cookie, err := request.Cookie(handler.cookieName())
	if err != nil {
		writeError(response, http.StatusUnauthorized, protocol.Unauthorized, "sign in required")
		return "", false
	}
	if !validSecret(cookie.Value) {
		handler.authFailLimit.take(key)
		handler.clearCookie(response)
		writeError(response, http.StatusUnauthorized, protocol.Unauthorized, "sign in required")
		return "", false
	}
	return cookie.Value, true
}

// mutation enforces the state-changing request boundary after the method
// check: exact Origin, the fixed request header, JSON content type, a 64 KiB
// body and strict decoding.
func (handler *gatewayHandler) mutation(response http.ResponseWriter, request *http.Request, destination any) bool {
	if !handler.originAllowed(request) {
		writeError(response, http.StatusForbidden, protocol.InvalidRequest, "origin not allowed")
		return false
	}
	if values := request.Header.Values(requestHeaderName); len(values) != 1 || values[0] != "1" {
		writeError(response, http.StatusForbidden, protocol.InvalidRequest, "missing request header")
		return false
	}
	if !jsonContentType(request.Header.Values("Content-Type")) {
		writeError(response, http.StatusUnsupportedMediaType, protocol.InvalidRequest, "content type must be application/json")
		return false
	}
	data, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maxRequestBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(response, http.StatusRequestEntityTooLarge, protocol.LimitExceeded, "request body too large")
		} else {
			writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "unreadable request body")
		}
		return false
	}
	if err := decodeStrict(data, destination); err != nil {
		writeError(response, http.StatusBadRequest, protocol.InvalidRequest, "malformed request body")
		return false
	}
	return true
}

func jsonContentType(values []string) bool {
	if len(values) != 1 {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || mediaType != "application/json" {
		return false
	}
	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

// decodeStrict accepts exactly one flat JSON object of known fields with
// valid UTF-8 and no duplicate keys. Every request body field is a string.
func decodeStrict(data []byte, destination any) error {
	if !utf8.Valid(data) {
		return errors.New("invalid UTF-8")
	}
	scanner := json.NewDecoder(bytes.NewReader(data))
	if token, err := scanner.Token(); err != nil || token != json.Delim('{') {
		return errors.New("body must be a JSON object")
	}
	seen := map[string]struct{}{}
	for scanner.More() {
		token, err := scanner.Token()
		if err != nil {
			return err
		}
		key, _ := token.(string)
		if _, duplicate := seen[key]; duplicate {
			return errors.New("duplicate key")
		}
		seen[key] = struct{}{}
		var value any
		if err := scanner.Decode(&value); err != nil {
			return err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

func (handler *gatewayHandler) cookieName() string {
	if handler.dev {
		return developmentCookieName
	}
	return productionCookieName
}

func (handler *gatewayHandler) setCookie(response http.ResponseWriter, credential string, expiresAt time.Time) {
	http.SetCookie(response, &http.Cookie{
		Name: handler.cookieName(), Value: credential, Path: "/",
		MaxAge:   int(math.Ceil(time.Until(expiresAt).Seconds())),
		Secure:   !handler.dev,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func (handler *gatewayHandler) clearCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name: handler.cookieName(), Value: "", Path: "/", MaxAge: -1,
		Secure: !handler.dev, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

func isAuthFailure(err error) bool {
	var brokerErr *gatewayapi.Error
	return errors.As(err, &brokerErr) && (brokerErr.Code == protocol.Unauthorized || brokerErr.Code == protocol.Revoked)
}

// brokerStatus is the documented public status mapping.
func brokerStatus(code protocol.ErrorCode) int {
	switch code {
	case protocol.Unauthorized, protocol.Revoked:
		return http.StatusUnauthorized
	case protocol.PairingExpired:
		return http.StatusGone
	case protocol.LimitExceeded:
		return http.StatusTooManyRequests
	case protocol.SessionMissing, protocol.ProjectMissing, protocol.ShellProfileMissing:
		return http.StatusNotFound
	case protocol.GenerationChanged, protocol.SessionLost, protocol.OperationConflict, protocol.PairingPending,
		protocol.NotController, protocol.StaleLease, protocol.InputGap, protocol.HistoryChanged, protocol.ResyncRequired:
		return http.StatusConflict
	case protocol.HostUnavailable:
		return http.StatusServiceUnavailable
	case protocol.InvalidRequest, protocol.ProtocolMismatch:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// writeBrokerError maps a broker error to its public form. Untyped errors are
// transport failures and never expose their text.
func writeBrokerError(response http.ResponseWriter, err error) {
	var brokerErr *gatewayapi.Error
	if !errors.As(err, &brokerErr) {
		writeError(response, http.StatusServiceUnavailable, protocol.HostUnavailable, "host unavailable")
		return
	}
	status := brokerStatus(brokerErr.Code)
	code := brokerErr.Code
	if status == http.StatusInternalServerError {
		code = protocol.InternalError
	}
	message := brokerErr.Message
	if !validText(message, maxErrorMessageBytes) || status == http.StatusInternalServerError {
		message = http.StatusText(status)
	}
	writeError(response, status, code, message)
}

func writeError(response http.ResponseWriter, status int, code protocol.ErrorCode, message string) {
	writeJSON(response, status, protocol.HTTPErrorResponse{
		Error: protocol.BrokerRPCErrorPayload{Code: code, Message: message},
	})
}

// validSecret mirrors the schema secret: 43 unpadded base64url characters.
func validSecret(value string) bool {
	if len(value) != 43 {
		return false
	}
	for index := 0; index < len(value); index++ {
		switch character := value[index]; {
		case character >= 'A' && character <= 'Z', character >= 'a' && character <= 'z',
			character >= '0' && character <= '9', character == '-', character == '_':
		default:
			return false
		}
	}
	return true
}

// validUUID mirrors the schema uuid pattern (lowercase, versions 1..8).
func validUUID(value string) bool {
	if len(value) != 36 || value[14] < '1' || value[14] > '8' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for index := 0; index < len(value); index++ {
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

// validText mirrors maxUtf8Bytes plus safeDisplayText. U+FFFD is rejected
// because encoding/json substitutes it for lone surrogate escapes.
func validText(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f || character >= 0x80 && character <= 0x9f || character == utf8.RuneError {
			return false
		}
	}
	return true
}

// clientKey identifies the rate-limit client by peer IP. Forwarded headers
// are ignored.
// ponytail: behind Tailscale Serve every peer is 127.0.0.1, so limits are
// effectively global; per-client limits need a trusted peer identity.
func clientKey(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return request.RemoteAddr
	}
	return host
}

const maxLimiterClients = 1024

// limiter is an in-memory token bucket per client with a bounded map.
type limiter struct {
	mu      sync.Mutex
	burst   float64
	every   time.Duration
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(burst int, every time.Duration) *limiter {
	return &limiter{burst: float64(burst), every: every, buckets: map[string]*bucket{}, now: time.Now}
}

// take consumes one token and reports whether one was available.
func (limit *limiter) take(key string) bool { return limit.check(key, true) }

// allowed reports whether a token is available without consuming it.
func (limit *limiter) allowed(key string) bool { return limit.check(key, false) }

func (limit *limiter) check(key string, consume bool) bool {
	limit.mu.Lock()
	defer limit.mu.Unlock()
	now := limit.now()
	current := limit.buckets[key]
	if current == nil {
		if !consume {
			return true
		}
		if len(limit.buckets) >= maxLimiterClients {
			for other, candidate := range limit.buckets {
				if limit.refill(candidate, now) >= limit.burst {
					delete(limit.buckets, other)
				}
			}
			if len(limit.buckets) >= maxLimiterClients {
				return false
			}
		}
		current = &bucket{tokens: limit.burst, last: now}
		limit.buckets[key] = current
	}
	if limit.refill(current, now) < 1 {
		return false
	}
	if consume {
		current.tokens--
	}
	return true
}

func (limit *limiter) refill(current *bucket, now time.Time) float64 {
	current.tokens = min(limit.burst, current.tokens+float64(now.Sub(current.last))/float64(limit.every))
	current.last = now
	return current.tokens
}
