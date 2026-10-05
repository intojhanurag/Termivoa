package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	openBody   = `{"targetId":"w8:p1.term_65d0031602b777","columns":45,"rows":20}`
	createBody = `{"groupId":"g3f9a0c2b17e4","name":"silver-meadow","agent":"claude","prompt":"fix the tests\n\tplease"}`
	manageBody = `{"action":"closeItem","itemId":"w3","name":"silver-meadow"}`
	managePath = "/api/v1/providers/herdr/manage"
)

func TestProviderRoutesReachTheBroker(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	for _, test := range []struct{ method, path, body, call, want string }{
		{http.MethodGet, "/api/v1/providers/herdr", "", "provider:herdr", `"provider":"herdr"`},
		{http.MethodPatch, "/api/v1/providers/herdr", `{"enabled":false,"notify":["done"]}`, "provider-update:herdr", `"id":"herdr"`},
		{http.MethodPost, "/api/v1/providers/herdr/open", openBody, "provider-open:w8:p1.term_65d0031602b777", `"targetId":"w8:p1.term_65d0031602b777"`},
		{http.MethodPost, "/api/v1/providers/herdr/create", createBody, "provider-create:silver-meadow", `"itemId":"w9"`},
		{http.MethodPost, managePath, manageBody, "provider-manage:closeItem", `"itemId":"w3"`},
		{http.MethodPost, managePath, `{"action":"removeItem","itemId":"w3","name":"silver-meadow"}`, "provider-manage:removeItem", `"itemId":"w3"`},
		{http.MethodPost, managePath, `{"action":"newTab","itemId":"w3"}`, "provider-manage:newTab", `"itemId":"w3"`},
		{http.MethodPost, managePath, `{"action":"newWorkspace","projectId":"81111111-1111-4111-8111-111111111111"}`, "provider-manage:newWorkspace", `{}`},
		{http.MethodPost, managePath, `{"action":"startServer"}`, "provider-manage:startServer", `{}`},
		{http.MethodPost, managePath, `{"action":"newTab","itemId":"w3","agent":"claude","prompt":"fix it"}`, "provider-manage:newTab", `"itemId":"w3"`},
		{http.MethodPost, managePath, `{"action":"openItem","groupId":"g7d0f77ccdcfd","worktree":"fix/old-idea"}`, "provider-manage:openItem", `{}`},
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

func TestProviderRoutesRefuseBadRequests(t *testing.T) {
	broker := newFakeBroker()
	handler := newTestHandler(t, broker)
	for _, test := range []struct {
		name, method, path, body string
		options                  requestOptions
		status                   int
	}{
		{"no cookie", http.MethodGet, "/api/v1/providers/herdr", "", requestOptions{}, 401},
		{"bad provider", http.MethodGet, "/api/v1/providers/Herdr", "", requestOptions{cookie: prodCookie}, 400},
		{"provider with a space", http.MethodPost, "/api/v1/providers/her%20dr/open", openBody, requestOptions{cookie: prodCookie}, 400},
		{"wrong origin", http.MethodGet, "/api/v1/providers/herdr", "", requestOptions{cookie: prodCookie, origin: "https://evil.tailnet.ts.net"}, 403},
		{"no-cors", http.MethodGet, "/api/v1/providers/herdr", "", requestOptions{cookie: prodCookie, omitOrigin: true,
			header: http.Header{"Sec-Fetch-Mode": {"no-cors"}, "Sec-Fetch-Site": {"same-origin"}}}, 403},
		{"missing header", http.MethodPost, "/api/v1/providers/herdr/open", openBody, requestOptions{cookie: prodCookie, omitHeader: true}, 403},
		{"text body", http.MethodPost, "/api/v1/providers/herdr/open", openBody, requestOptions{cookie: prodCookie, contentType: "text/plain"}, 415},
		{"unknown field", http.MethodPost, "/api/v1/providers/herdr/open", `{"targetId":"w1:p1.t","columns":45,"rows":20,"takeover":true}`, requestOptions{cookie: prodCookie}, 400},
		{"oversize", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"` + strings.Repeat("a", 70*1024) + `"}`, requestOptions{cookie: prodCookie}, 413},
		{"narrow grid", http.MethodPost, "/api/v1/providers/herdr/open", `{"targetId":"w1:p1.t","columns":10,"rows":20}`, requestOptions{cookie: prodCookie}, 400},
		{"target path", http.MethodPost, "/api/v1/providers/herdr/open", `{"targetId":"/etc/passwd","columns":45,"rows":20}`, requestOptions{cookie: prodCookie}, 400},
		{"target flag", http.MethodPost, "/api/v1/providers/herdr/open", `{"targetId":"--takeover","columns":45,"rows":20}`, requestOptions{cookie: prodCookie}, 400},
		{"empty update", http.MethodPatch, "/api/v1/providers/herdr", `{}`, requestOptions{cookie: prodCookie}, 400},
		{"unknown notify", http.MethodPatch, "/api/v1/providers/herdr", `{"notify":["working"]}`, requestOptions{cookie: prodCookie}, 400},
		{"bad name", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"Bad Name"}`, requestOptions{cookie: prodCookie}, 400},
		{"lock name", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"fix/x.lock"}`, requestOptions{cookie: prodCookie}, 400},
		{"five parts", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"a/b/c/d/e"}`, requestOptions{cookie: prodCookie}, 400},
		{"escape in prompt", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"a","prompt":"hi\u001b[2J"}`, requestOptions{cookie: prodCookie}, 400},
		{"c1 in prompt", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"a","prompt":"hi\u009b"}`, requestOptions{cookie: prodCookie}, 400},
		{"bad agent", http.MethodPost, "/api/v1/providers/herdr/create", `{"groupId":"g1","name":"a","agent":"sh -c x"}`, requestOptions{cookie: prodCookie}, 400},
		{"GET open", http.MethodGet, "/api/v1/providers/herdr/open", "", requestOptions{cookie: prodCookie}, 405},
		{"GET manage", http.MethodGet, managePath, "", requestOptions{cookie: prodCookie}, 405},
		{"manage without a cookie", http.MethodPost, managePath, manageBody, requestOptions{}, 401},
		{"manage from another origin", http.MethodPost, managePath, manageBody, requestOptions{cookie: prodCookie, origin: "https://evil.tailnet.ts.net"}, 403},
		{"manage without the header", http.MethodPost, managePath, manageBody, requestOptions{cookie: prodCookie, omitHeader: true}, 403},
		{"manage as text", http.MethodPost, managePath, manageBody, requestOptions{cookie: prodCookie, contentType: "text/plain"}, 415},
		{"manage oversize", http.MethodPost, managePath, `{"action":"newTab","itemId":"` + strings.Repeat("a", 70*1024) + `"}`, requestOptions{cookie: prodCookie}, 413},
		{"manage unknown action", http.MethodPost, managePath, `{"action":"forceRemove","itemId":"w3","name":"a"}`, requestOptions{cookie: prodCookie}, 400},
		{"manage unknown field", http.MethodPost, managePath, `{"action":"removeItem","itemId":"w3","name":"a","force":true}`, requestOptions{cookie: prodCookie}, 400},
		{"close without the confirmed name", http.MethodPost, managePath, `{"action":"closeItem","itemId":"w3"}`, requestOptions{cookie: prodCookie}, 400},
		{"new tab with a name", http.MethodPost, managePath, `{"action":"newTab","itemId":"w3","name":"a"}`, requestOptions{cookie: prodCookie}, 400},
		{"a prompt without an agent", http.MethodPost, managePath, `{"action":"newTab","itemId":"w3","prompt":"go"}`, requestOptions{cookie: prodCookie}, 400},
		{"an agent on a close", http.MethodPost, managePath, `{"action":"closeItem","itemId":"w3","name":"a","agent":"claude"}`, requestOptions{cookie: prodCookie}, 400},
		{"an agent that is a command", http.MethodPost, managePath, `{"action":"newTab","itemId":"w3","agent":"claude --yolo"}`, requestOptions{cookie: prodCookie}, 400},
		{"open a path", http.MethodPost, managePath, `{"action":"openItem","groupId":"g7d0f77ccdcfd","worktree":"../shop"}`, requestOptions{cookie: prodCookie}, 400},
		{"open without a group", http.MethodPost, managePath, `{"action":"openItem","worktree":"old-idea"}`, requestOptions{cookie: prodCookie}, 400},
		{"new workspace with a path", http.MethodPost, managePath, `{"action":"newWorkspace","projectId":"/etc"}`, requestOptions{cookie: prodCookie}, 400},
		{"new workspace with an item", http.MethodPost, managePath, `{"action":"newWorkspace","projectId":"81111111-1111-4111-8111-111111111111","itemId":"w3"}`, requestOptions{cookie: prodCookie}, 400},
		{"start with a field", http.MethodPost, managePath, `{"action":"startServer","itemId":"w3"}`, requestOptions{cookie: prodCookie}, 400},
		{"item flag", http.MethodPost, managePath, `{"action":"newTab","itemId":"--force"}`, requestOptions{cookie: prodCookie}, 400},
		{"control in the name", http.MethodPost, managePath, `{"action":"closeItem","itemId":"w3","name":"a\u001b[2J"}`, requestOptions{cookie: prodCookie}, 400},
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
		if call != "provider:herdr" {
			t.Fatalf("a refused request reached the broker: %v", broker.calls)
		}
	}
}

func TestProviderRoutesCORSForBoundOrigins(t *testing.T) {
	handler := newTestHandler(t, newFakeBroker())
	home := "https://phone-home.tailnet.ts.net"
	handler.mu.Lock()
	handler.bound = originSet([]string{home}, protocol.MaxBoundOrigins)
	handler.mu.Unlock()
	response := do(handler, http.MethodGet, "/api/v1/providers/herdr", "", requestOptions{cookie: prodCookie, origin: home})
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != home {
		t.Fatalf("bound origin = %d %q", response.Code, response.Header().Get("Access-Control-Allow-Origin"))
	}
	response = do(handler, http.MethodPost, "/api/v1/providers/herdr/open", openBody, requestOptions{cookie: prodCookie, origin: home})
	if response.Code != http.StatusOK {
		t.Fatalf("bound open = %d %s", response.Code, response.Body)
	}
	response = do(handler, http.MethodPost, managePath, manageBody, requestOptions{cookie: prodCookie, origin: home})
	if response.Code != http.StatusOK {
		t.Fatalf("bound manage = %d %s", response.Code, response.Body)
	}
	response = do(handler, http.MethodGet, "/api/v1/providers/herdr", "", requestOptions{cookie: prodCookie, origin: "https://evil.tailnet.ts.net"})
	if response.Code != http.StatusForbidden || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unbound origin = %d", response.Code)
	}
}

func TestProviderErrorsUseTheStatusMapping(t *testing.T) {
	for _, test := range []struct {
		code   protocol.ErrorCode
		status int
	}{
		{protocol.InvalidRequest, 400}, {protocol.HostUnavailable, 503}, {protocol.SessionMissing, 404},
		{protocol.LimitExceeded, 429}, {protocol.NotARepository, 409}, {protocol.WorktreeExists, 409},
		{protocol.OperationConflict, 409}, {protocol.ProjectMissing, 404},
		{protocol.WorktreeFailed, 422},
	} {
		manage := newFakeBroker()
		manage.opErr = &gatewayapi.Error{Code: test.code, Message: "herdr could not remove the worktree."}
		if response := do(newTestHandler(t, manage), http.MethodPost, managePath, manageBody, requestOptions{cookie: prodCookie}); response.Code != test.status ||
			errorCode(t, response) != test.code || !strings.Contains(response.Body.String(), "herdr could not remove the worktree.") {
			t.Fatalf("manage %s = %d %s", test.code, response.Code, response.Body)
		}
		broker := newFakeBroker()
		broker.opErr = &gatewayapi.Error{Code: test.code, Message: "herdr could not make the worktree."}
		response := do(newTestHandler(t, broker), http.MethodPost, "/api/v1/providers/herdr/create", createBody, requestOptions{cookie: prodCookie})
		if response.Code != test.status || errorCode(t, response) != test.code {
			t.Fatalf("%s = %d %s", test.code, response.Code, response.Body)
		}
	}
}
