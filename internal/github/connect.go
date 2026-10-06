package github

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var (
	userCodePattern = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
	loginPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
)

// connectRun is the one running device flow of this computer.
type connectRun struct {
	device     string
	generation uint64 // Service.generation when it started
	userCode   string
	expires    time.Time
	cancel     context.CancelFunc
}

// maxConnect bounds GitHub's expires_in (15 minutes today).
const maxConnect = 30 * time.Minute

// Connect starts the device flow for device, or answers the running one
// (ErrBusy when another device started it). A computer that is connected
// answers its state and starts nothing. A new Connect clears the last problem.
func (service *Service) Connect(ctx context.Context, device string) (protocol.HTTPGitHubResponse, error) {
	if service == nil {
		return protocol.HTTPGitHubResponse{}, ErrNotConnected
	}
	service.tokenMu.Lock()
	generation := service.generation
	service.tokenMu.Unlock()
	service.mu.Lock()
	switch {
	case service.closed:
		service.mu.Unlock()
		return service.State(device), ErrGitHubDown
	case service.connect != nil && service.connect.device != device:
		service.mu.Unlock()
		return service.State(device), ErrBusy
	case service.connect != nil || service.account != nil:
		service.mu.Unlock()
		return service.State(device), nil
	}
	runCtx, cancel := context.WithCancel(service.ctx)
	run := &connectRun{device: device, generation: generation, cancel: cancel}
	service.connect = run // reserved while GitHub answers
	service.problem = nil
	service.mu.Unlock()

	var code struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		ExpiresIn  int64  `json:"expires_in"`
		Interval   int64  `json:"interval"`
	}
	// The service's context, not the phone's: a phone that drops does not
	// end the Connect (the client's 10 s limit still applies).
	status, err := service.do(runCtx, request{
		method: http.MethodPost, url: service.opts.web + "/login/device/code",
		form: url.Values{"client_id": {service.config.ClientID}}, login: true,
	}, &code)
	expires := time.Duration(min(max(code.ExpiresIn, 1), int64(maxConnect/time.Second))) * service.opts.second
	if err != nil || status != http.StatusOK || code.DeviceCode == "" || !userCodePattern.MatchString(code.UserCode) {
		cancelled := runCtx.Err() != nil // Cancel, Disconnect or Close during the request
		cancel()
		if cancelled {
			return service.State(device), nil
		}
		service.finishConnect(run, protocol.HTTPGitHubProblemGitHubDown)
		return service.State(device), ErrGitHubDown
	}
	service.mu.Lock()
	if service.connect != run || service.closed { // Cancel or Close came first.
		service.mu.Unlock()
		cancel()
		return service.State(device), nil
	}
	run.userCode, run.expires = code.UserCode, service.now().Add(expires)
	service.wg.Add(1)
	service.mu.Unlock()
	go func() {
		defer service.wg.Done()
		service.poll(runCtx, run, code.DeviceCode, max(code.Interval, 1))
	}()
	return service.State(device), nil
}

// poll asks GitHub for the token at the interval it gave until a token, a
// refusal, the expiry, Cancel or Close. A network failure keeps polling.
func (service *Service) poll(ctx context.Context, run *connectRun, deviceCode string, interval int64) {
	defer run.cancel()
	timer := time.NewTimer(time.Duration(interval) * service.opts.second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			service.finishConnect(run, "")
			return
		case <-timer.C:
		}
		if !service.now().Before(run.expires) {
			service.finishConnect(run, protocol.CodeExpired)
			return
		}
		var answer tokenAnswer
		_, err := service.do(ctx, request{
			method: http.MethodPost, url: service.opts.web + "/login/oauth/access_token", login: true,
			form: url.Values{
				"client_id": {service.config.ClientID}, "device_code": {deviceCode},
				"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"},
			},
		}, &answer)
		switch {
		case err != nil || answer.Error == "authorization_pending":
		case answer.Error == "slow_down":
			interval += 5
			if answer.Interval > 0 {
				interval = answer.Interval
			}
		case answer.Error == "expired_token" || answer.Error == "token_expired" || answer.Error == "incorrect_device_code":
			service.finishConnect(run, protocol.CodeExpired)
			return
		case answer.Error == "access_denied":
			service.finishConnect(run, protocol.Denied)
			return
		case answer.Error != "" || answer.AccessToken == "":
			// device_flow_disabled, incorrect_client_credentials,
			// unsupported_grant_type: the app is misconfigured, which the
			// phone can only show as "cannot reach GitHub".
			service.finishConnect(run, protocol.HTTPGitHubProblemGitHubDown)
			return
		default:
			// The user authorized: finish even when Cancel comes now, so
			// that a granted login is never dropped. Close still ends it.
			service.finishConnect(run, service.login(service.ctx, answer, run.generation))
			return
		}
		timer.Reset(time.Duration(interval) * service.opts.second)
	}
}

// login reads the account of a new token pair, saves it, and reads the
// installations. It returns the problem, or "" on success.
// A Disconnect since the Connect started (generation changed) wins: the new
// pair is revoked and not saved.
func (service *Service) login(ctx context.Context, answer tokenAnswer, generation uint64) protocol.HTTPGitHubProblem {
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	status, err := service.do(ctx, request{method: http.MethodGet, url: service.opts.api + "/user", token: answer.AccessToken}, &user)
	if err != nil || status != http.StatusOK || !loginPattern.MatchString(user.Login) || user.ID <= 0 {
		return protocol.HTTPGitHubProblemGitHubDown
	}
	acc := answer.pair(service.now(), nil)
	acc.login, acc.id = user.Login, user.ID
	service.tokenMu.Lock()
	if service.generation != generation {
		service.revoke(ctx, acc)
		service.tokenMu.Unlock()
		return ""
	}
	err = saveAccount(service.config.DataDir, acc)
	if err == nil {
		service.mu.Lock()
		service.account = acc
		service.installKnown = false
		service.dropCache()
		service.mu.Unlock()
	}
	service.tokenMu.Unlock()
	if err != nil {
		return protocol.HTTPGitHubProblemGitHubDown
	}
	service.checkInstalls(ctx)
	return ""
}

// finishConnect ends run (when it is still the running one) with problem.
func (service *Service) finishConnect(run *connectRun, problem protocol.HTTPGitHubProblem) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.connect != run {
		return
	}
	service.connect = nil
	if problem != "" {
		service.problem = &problem
	}
}

// Cancel ends a Connect that device started and that is not finished; for
// another device it changes nothing. It never touches a stored login.
func (service *Service) Cancel(device string) protocol.HTTPGitHubResponse {
	if service == nil {
		return protocol.HTTPGitHubResponse{}
	}
	service.cancelConnect(device, false)
	return service.State(device)
}

func (service *Service) cancelConnect(device string, anyDevice bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if run := service.connect; run != nil && (anyDevice || run.device == device) {
		run.cancel()
		service.connect = nil
	}
}

// Disconnect revokes both tokens at GitHub (best effort, 10 s) and deletes
// the token file. revoked is false when the revoke call failed. A running
// Connect is cancelled too.
func (service *Service) Disconnect(ctx context.Context, device string) (response protocol.HTTPGitHubResponse, revoked bool, err error) {
	if service == nil {
		return protocol.HTTPGitHubResponse{}, false, ErrNotConnected
	}
	service.cancelConnect(device, true)
	service.tokenMu.Lock()
	service.generation++
	acc := service.current()
	revoked = true
	if acc != nil {
		revoked = service.revoke(ctx, acc)
	}
	service.mu.Lock()
	err = deleteAccount(service.config.DataDir)
	service.account, service.problem, service.installKnown, service.needsInstall = nil, nil, false, false
	service.dropCache()
	service.mu.Unlock()
	service.tokenMu.Unlock()
	return service.State(device), revoked, err
}

// revoke sends both tokens of acc to POST /credentials/revoke (no
// authentication). It reports whether GitHub accepted them.
func (service *Service) revoke(ctx context.Context, acc *account) bool {
	credentials := []string{acc.access.reveal()}
	if refresh := acc.refresh.reveal(); refresh != "" {
		credentials = append(credentials, refresh)
	}
	status, err := service.do(ctx, request{
		method: http.MethodPost, url: service.opts.api + "/credentials/revoke",
		json: map[string][]string{"credentials": credentials},
	}, nil)
	return err == nil && status/100 == 2
}
