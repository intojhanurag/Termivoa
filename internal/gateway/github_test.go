package gateway

import (
	"net/http"
	"strings"
	"testing"
)

const githubPath = "/api/v1/github"

func TestGitHubRoutesReachTheBroker(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	for _, test := range []struct{ method, path, body, call, want string }{
		{http.MethodGet, githubPath, "", "github", `"app":"termivoa"`},
		{http.MethodPost, githubPath, `{"action":"connect"}`, "github-action:connect", `"state":"notConnected"`},
		{http.MethodPost, githubPath, `{"action":"setup","repo":"octo/shop"}`, "github-action:setup", `"app":"termivoa"`},
		{http.MethodPost, githubPath, `{"action":"stop"}`, "github-action:stop", `"app":"termivoa"`},
		{http.MethodGet, githubPath + "/repos", "", "github-repos", `"repos":[]`},
	} {
		response := do(handler, test.method, test.path, test.body, requestOptions{cookie: prodCookie})
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) || !broker.called(test.call) {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s is cacheable", test.method, test.path)
		}
	}
}

func TestGitHubRoutesRefuseBadRequests(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	for _, test := range []struct {
		name, method, path, body string
		options                  requestOptions
		status                   int
	}{
		{"no cookie", http.MethodGet, githubPath, "", requestOptions{}, 401},
		{"repos no cookie", http.MethodGet, githubPath + "/repos", "", requestOptions{}, 401},
		{"post no cookie", http.MethodPost, githubPath, `{"action":"connect"}`, requestOptions{}, 401},
		{"repo on connect", http.MethodPost, githubPath, `{"action":"connect","repo":"octo/shop"}`, requestOptions{cookie: prodCookie}, 400},
		{"setup without repo", http.MethodPost, githubPath, `{"action":"setup"}`, requestOptions{cookie: prodCookie}, 400},
		{"repo ending .git", http.MethodPost, githubPath, `{"action":"setup","repo":"octo/shop.git"}`, requestOptions{cookie: prodCookie}, 400},
		{"unknown action", http.MethodPost, githubPath, `{"action":"push"}`, requestOptions{cookie: prodCookie}, 400},
		{"unknown field", http.MethodPost, githubPath, `{"action":"connect","force":true}`, requestOptions{cookie: prodCookie}, 400},
		{"PATCH", http.MethodPatch, githubPath, `{"action":"connect"}`, requestOptions{cookie: prodCookie}, 405},
		{"POST repos", http.MethodPost, githubPath + "/repos", `{}`, requestOptions{cookie: prodCookie}, 405},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := do(handler, test.method, test.path, test.body, test.options)
			if response.Code != test.status {
				t.Fatalf("status = %d body = %s", response.Code, response.Body)
			}
		})
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for _, call := range broker.calls {
		if strings.HasPrefix(call, "github") {
			t.Fatalf("a refused request reached the broker: %v", broker.calls)
		}
	}
}
