package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// secret is a GitHub token. Every ordinary way to print or encode it gives
// [REDACTED]; reveal is the one explicit way out. The pointer keeps fmt's
// reflection fallback (an unexported field of a bigger struct) at an address.
type secret struct{ value *string }

func newSecret(value string) secret { return secret{value: &value} }

func (s secret) reveal() string {
	if s.value == nil {
		return ""
	}
	return *s.value
}

var errSecretSerialization = errors.New("github: a token is never encoded")

func (secret) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, "[REDACTED]") }
func (secret) String() string                 { return "[REDACTED]" }
func (secret) GoString() string               { return "github.secret([REDACTED])" }
func (secret) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }
func (secret) MarshalJSON() ([]byte, error)   { return nil, errSecretSerialization }
func (secret) MarshalText() ([]byte, error)   { return nil, errSecretSerialization }

// account is the one login of this computer. A zero expiry means GitHub
// gave none (an app without expiring tokens).
type account struct {
	login                       string
	id                          int64
	access, refresh             secret
	accessExpiry, refreshExpiry time.Time
	updated                     time.Time
}

// tokenFile is the only encoded form of an account: DataDir/github.json.
type tokenFile struct {
	Login         string `json:"login"`
	ID            int64  `json:"id"`
	AccessToken   string `json:"accessToken"`
	AccessExpiry  int64  `json:"accessExpiry,omitempty"`
	RefreshToken  string `json:"refreshToken,omitempty"`
	RefreshExpiry int64  `json:"refreshExpiry,omitempty"`
	Updated       int64  `json:"updated"`
}

const tokenFileName = "github.json"

func unixOrZero(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return at.Unix()
}

func timeOrZero(seconds int64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

// loadAccount reads the token file: nil, nil when there is none. A file
// that does not parse is an error (the broker reports it; it is not
// deleted, so nothing the user can recover is lost).
func loadAccount(dataDir string) (*account, error) {
	path := filepath.Join(dataDir, tokenFileName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err := ipc.EnsurePrivateFile(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file tokenFile
	if err := json.Unmarshal(data, &file); err != nil || !loginPattern.MatchString(file.Login) || file.AccessToken == "" {
		return nil, errors.New("github: the token file is damaged")
	}
	return &account{
		login: file.Login, id: file.ID,
		access: newSecret(file.AccessToken), refresh: newSecret(file.RefreshToken),
		accessExpiry: timeOrZero(file.AccessExpiry), refreshExpiry: timeOrZero(file.RefreshExpiry),
		updated: timeOrZero(file.Updated),
	}, nil
}

// saveAccount writes the file atomically: a 0600 temp file in dataDir,
// fsync, rename.
func saveAccount(dataDir string, acc *account) error {
	data, err := json.Marshal(tokenFile{
		Login: acc.login, ID: acc.id,
		AccessToken: acc.access.reveal(), AccessExpiry: unixOrZero(acc.accessExpiry),
		RefreshToken: acc.refresh.reveal(), RefreshExpiry: unixOrZero(acc.refreshExpiry),
		Updated: unixOrZero(acc.updated),
	})
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dataDir, ".github-*.tmp") // mode 0600
	if err != nil {
		return err
	}
	name := temp.Name()
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(dataDir, tokenFileName))
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// deleteAccount overwrites the file's bytes with zeros, syncs and removes
// it. No file is not an error.
func deleteAccount(dataDir string) error {
	path := filepath.Join(dataDir, tokenFileName)
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info, statErr := file.Stat(); statErr == nil {
		_, _ = file.Write(make([]byte, info.Size()))
		_ = file.Sync()
	}
	_ = file.Close()
	return os.Remove(path)
}

// tokenAnswer is GitHub's answer on /login/oauth/access_token.
type tokenAnswer struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	Error                 string `json:"error"`
	Interval              int64  `json:"interval"`
}

func (answer tokenAnswer) pair(now time.Time, base *account) *account {
	next := &account{access: newSecret(answer.AccessToken), refresh: newSecret(answer.RefreshToken), updated: now}
	if base != nil {
		next.login, next.id = base.login, base.id
	}
	if answer.ExpiresIn > 0 {
		next.accessExpiry = now.Add(time.Duration(answer.ExpiresIn) * time.Second)
	}
	if answer.RefreshTokenExpiresIn > 0 {
		next.refreshExpiry = now.Add(time.Duration(answer.RefreshTokenExpiresIn) * time.Second)
	}
	return next
}

const refreshMargin = 5 * time.Minute

// fresh returns the account with an access token that has at least 5
// minutes left, refreshing it first under tokenMu. A refresh commits the
// new pair to the file before it returns it (design 6.2): GitHub has
// already killed the old pair.
func (service *Service) fresh(ctx context.Context) (*account, error) {
	service.tokenMu.Lock()
	defer service.tokenMu.Unlock()
	acc := service.current()
	if acc == nil {
		return nil, ErrNotConnected
	}
	now := service.now()
	if acc.accessExpiry.IsZero() || acc.accessExpiry.Sub(now) >= refreshMargin {
		return acc, nil
	}
	if acc.refresh.reveal() == "" || !acc.refreshExpiry.IsZero() && !now.Before(acc.refreshExpiry) {
		service.end(acc, protocol.Ended)
		return nil, ErrNotConnected
	}
	form := url.Values{"client_id": {service.config.ClientID}, "grant_type": {"refresh_token"}, "refresh_token": {acc.refresh.reveal()}}
	call := request{method: http.MethodPost, url: service.opts.web + "/login/oauth/access_token", form: form, login: true}
	var answer tokenAnswer
	_, err := service.do(ctx, call, &answer)
	if errors.Is(err, errTransport) && ctx.Err() == nil {
		// The answer may be lost after GitHub rotated: one more try with the
		// same refresh token (design 6.2).
		answer = tokenAnswer{}
		_, err = service.do(ctx, call, &answer)
	}
	if err != nil {
		return nil, ErrGitHubDown
	}
	switch {
	case answer.Error == "bad_refresh_token":
		problem := protocol.OtherProgram
		if !acc.refreshExpiry.IsZero() && acc.refreshExpiry.Sub(service.now()) < time.Minute {
			problem = protocol.Ended
		}
		service.end(acc, problem)
		return nil, ErrNotConnected
	case answer.Error != "" || answer.AccessToken == "":
		return nil, ErrGitHubDown
	}
	next := answer.pair(service.now(), acc)
	if err := saveAccount(service.config.DataDir, next); err != nil {
		// ponytail: the new pair is lost when the file cannot be written;
		// the user connects again. Nothing better exists without storage.
		service.end(acc, protocol.Ended)
		return nil, err
	}
	service.mu.Lock()
	if service.account == acc {
		service.account = next
	}
	service.mu.Unlock()
	return next, nil
}

// end deletes the login acc (when it is still the current one) and records
// problem: the row then says Connect.
func (service *Service) end(acc *account, problem protocol.HTTPGitHubProblem) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.account != acc {
		return
	}
	_ = deleteAccount(service.config.DataDir)
	service.account = nil
	service.problem = &problem
	service.dropCache()
}

// Credential is the answer of `pterm github-credential get`: the login, a
// fresh access token (refreshed when it has less than 5 minutes left) and
// its expiry (zero when GitHub gave none). Never the refresh token.
func (service *Service) Credential(ctx context.Context) (login, token string, expires time.Time, err error) {
	if service == nil {
		return "", "", time.Time{}, ErrNotConnected
	}
	acc, err := service.fresh(ctx)
	if err != nil {
		return "", "", time.Time{}, err
	}
	return acc.login, acc.access.reveal(), acc.accessExpiry, nil
}

// apiGet is a GET on the API with the fresh token. A 401 with a token this
// computer believes fresh means the login was removed on GitHub: the file
// is deleted (problem ended) and the answer is ErrNotConnected.
func (service *Service) apiGet(ctx context.Context, path string, out any) (int, error) {
	acc, err := service.fresh(ctx)
	if err != nil {
		return 0, err
	}
	status, err := service.do(ctx, request{method: http.MethodGet, url: service.opts.api + path, token: acc.access.reveal()}, out)
	if err != nil {
		return status, ErrGitHubDown
	}
	if status == http.StatusUnauthorized {
		service.end(acc, protocol.Ended)
		return status, ErrNotConnected
	}
	return status, nil
}
