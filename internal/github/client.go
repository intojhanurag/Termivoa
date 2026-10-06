package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// options are the hosts and limits of one Service. A release build always
// uses defaultOptions as written here; only github_e2e.go (build tag e2e)
// and the tests change them.
type options struct {
	// web serves the login endpoints, api the REST API, git the clones.
	web, api, git string
	// open allows http and loopback or private addresses (tests and e2e).
	open bool
	// second is one GitHub "second" of the device flow (tests shorten it).
	second         time.Duration
	requestTimeout time.Duration
	cloneLimit     time.Duration
}

var defaultOptions = options{
	web: "https://github.com", api: "https://api.github.com", git: "https://github.com",
	second: time.Second, requestTimeout: 10 * time.Second, cloneLimit: 30 * time.Minute,
}

const (
	userAgent    = "Termivoa"
	maxBodyBytes = 1 << 20
)

var errHost = errors.New("github: refusing a host that is not GitHub")

func newHTTPClient(opts options) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !opts.open {
		dialer.Control = publicAddressOnly
	}
	return &http.Client{
		Timeout: opts.requestTimeout,
		Transport: &http.Transport{
			// ponytail: no proxy support, as webpush; a host behind a
			// mandatory proxy cannot reach GitHub.
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: opts.requestTimeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
			ForceAttemptHTTP2:     true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// publicAddressOnly is webpush's dial check: GitHub is never on a loopback,
// private, link-local or CGNAT (Tailscale) address.
func publicAddressOnly(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	ip := addrPort.Addr().Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return fmt.Errorf("github: refusing non-public address %s", ip)
	}
	return nil
}

// request is one call to GitHub.
type request struct {
	method, url string
	form        url.Values // login endpoints
	json        any        // API body
	token       string     // Authorization: Bearer, when not empty
	login       bool       // Accept: application/json instead of the API headers
}

// do sends request and decodes a JSON answer into out (when out is not nil
// and the status is 2xx, or the request is a login one). The error is
// ErrGitHubDown for a network failure, a 5xx, a rate limit or a bad body;
// other statuses are the caller's.
func (service *Service) do(ctx context.Context, call request, out any) (int, error) {
	target, err := url.Parse(call.url)
	if err != nil || !service.allowed(target) {
		return 0, errHost
	}
	var body io.Reader
	switch {
	case call.form != nil:
		body = strings.NewReader(call.form.Encode())
	case call.json != nil:
		encoded, err := json.Marshal(call.json)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(encoded)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, call.method, call.url, body)
	if err != nil {
		return 0, errHost
	}
	httpRequest.Header.Set("User-Agent", userAgent)
	if call.login {
		httpRequest.Header.Set("Accept", "application/json")
	} else {
		httpRequest.Header.Set("Accept", "application/vnd.github+json")
		httpRequest.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	switch {
	case call.form != nil:
		httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	case call.json != nil:
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if call.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+call.token)
	}
	response, err := service.client.Do(httpRequest)
	if err != nil {
		return 0, errors.Join(ErrGitHubDown, errTransport)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil {
		return response.StatusCode, errors.Join(ErrGitHubDown, errTransport)
	}
	status := response.StatusCode
	if status >= 500 || status == http.StatusTooManyRequests ||
		status == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0" {
		return status, ErrGitHubDown
	}
	if len(data) > maxBodyBytes {
		return status, ErrGitHubDown
	}
	if out != nil && (call.login || status/100 == 2) {
		if err := json.Unmarshal(data, out); err != nil {
			return status, ErrGitHubDown
		}
	}
	return status, nil
}

// errTransport marks a network failure (no answer), which a refresh tries
// one more time.
var errTransport = errors.New("github: no answer")

// allowed: the URL is on one of the two GitHub hosts, with their scheme.
func (service *Service) allowed(target *url.URL) bool {
	for _, base := range []string{service.opts.web, service.opts.api} {
		parsed, err := url.Parse(base)
		if err == nil && target.Scheme == parsed.Scheme && strings.EqualFold(target.Host, parsed.Host) && target.User == nil {
			return true
		}
	}
	return false
}
