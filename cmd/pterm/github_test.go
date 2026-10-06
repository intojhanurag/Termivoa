package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestGitHubCredentialHelper(t *testing.T) {
	const answer = "username=octo\npassword=ghu_token\npassword_expiry_utc=1790000000\n"
	ok := func(context.Context) (protocol.AdminRPCGitHubCredentialPayload, error) {
		return protocol.AdminRPCGitHubCredentialPayload{Username: "octo", Password: "ghu_token", ExpiresAt: 1790000000}, nil
	}
	down := func(context.Context) (protocol.AdminRPCGitHubCredentialPayload, error) {
		return protocol.AdminRPCGitHubCredentialPayload{}, errors.New("host_unavailable")
	}
	github := "protocol=https\nhost=github.com\n"
	for _, test := range []struct {
		name, op, input, stdout, stderr string
		code                            int
		down                            bool
	}{
		{name: "blank line ends", op: "get", input: github + "\nhost=evil.com\n", stdout: answer},
		{name: "EOF ends without newline", op: "get", input: "protocol=https\nhost=github.com", stdout: answer},
		{name: "unknown keys ignored", op: "get", input: "wwwauth[]=Basic\n" + github + "path=a/b.git\n", stdout: answer},
		{name: "wrong host", op: "get", input: "protocol=https\nhost=github.com.evil.com\n"},
		{name: "host with port", op: "get", input: "protocol=https\nhost=github.com:443\n"},
		{name: "wrong protocol", op: "get", input: "protocol=http\nhost=github.com\n"},
		{name: "store silent", op: "store", input: github + "username=x\npassword=y\n"},
		{name: "erase silent", op: "erase", input: github},
		{name: "unknown op silent", op: "other", input: github},
		{name: "not connected", op: "get", input: github, down: true, code: 1, stdout: "quit=1\n", stderr: githubNotConnected + "\n"},
		{name: "too many lines", op: "get", input: strings.Repeat("a=b\n", 33), code: 1},
		{name: "32 lines fit", op: "get", input: strings.Repeat("a=b\n", 30) + github, stdout: answer},
		{name: "too many bytes", op: "get", input: "a=" + strings.Repeat("x", 64<<10) + "\n", code: 1},
		{name: "no equals", op: "get", input: "protocol\n", code: 1},
		{name: "NUL", op: "get", input: "protocol=https\x00\nhost=github.com\n", code: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := ok
			if test.down {
				source = down
			}
			var stdout, stderr bytes.Buffer
			code := runGitHubCredential(context.Background(), []string{test.op}, strings.NewReader(test.input), &stdout, &stderr, source)
			if code != test.code || stdout.String() != test.stdout || stderr.String() != test.stderr {
				t.Fatalf("code %d stdout %q stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestGitHubCredentialFlags(t *testing.T) {
	for _, test := range []struct {
		args       []string
		rest       []string
		dev        bool
		runtimeDir string
	}{
		{[]string{"get"}, []string{"get"}, false, ""},
		{[]string{"--dev", "get"}, []string{"get"}, true, ""},
		{[]string{"--dev", "--runtime-dir", "/tmp/r", "get"}, []string{"get"}, true, "/tmp/r"},
		{[]string{"--runtime-dir"}, []string{"--runtime-dir"}, false, ""},
	} {
		rest, dev, runtimeDir := githubCredentialFlags(test.args)
		if !slices.Equal(rest, test.rest) || dev != test.dev || runtimeDir != test.runtimeDir {
			t.Errorf("%q: %q %v %q", test.args, rest, dev, runtimeDir)
		}
	}
}

func TestGitHubStatusLine(t *testing.T) {
	login := "octo"
	state := func(value protocol.HTTPGitHubState) *protocol.HTTPGitHubState { return &value }
	for _, test := range []struct {
		status protocol.AdminRPCGitHubStatusPayload
		want   string
	}{
		{protocol.AdminRPCGitHubStatusPayload{}, "GitHub: not available in this build"},
		{protocol.AdminRPCGitHubStatusPayload{State: state(protocol.HTTPGitHubStateReady), Login: &login}, "GitHub: connected as octo"},
		{protocol.AdminRPCGitHubStatusPayload{State: state(protocol.NeedsInstall), Login: &login}, "GitHub: connected as octo; choose repositories on GitHub"},
		{protocol.AdminRPCGitHubStatusPayload{State: state(protocol.HTTPGitHubStateNotConnected)}, "GitHub: not connected"},
		{protocol.AdminRPCGitHubStatusPayload{State: state(protocol.Connecting)}, "GitHub: connecting"},
		{protocol.AdminRPCGitHubStatusPayload{State: state(protocol.NoGit)}, "GitHub: git is not installed on this computer"},
	} {
		if got := githubStatusLine(test.status); got != test.want {
			t.Errorf("%+v: %q, want %q", test.status, got, test.want)
		}
	}
	var output bytes.Buffer
	if err := writeGitHubDisconnect(protocol.AdminRPCGitHubStatusPayload{State: state(protocol.HTTPGitHubStateNotConnected)}, &output); err != nil ||
		output.String() != "GitHub: disconnected\n"+githubRevokeHint+"\n" {
		t.Fatalf("disconnect = %q, %v", output.String(), err)
	}
}

func TestGitHubConfig(t *testing.T) {
	config := githubConfig()
	if runtime.GOOS == "windows" {
		if config != nil {
			t.Fatal("Windows has no GitHub feature")
		}
		return
	}
	if config == nil || !filepath.IsAbs(config.Pterm) || !filepath.IsAbs(config.Home) || len(config.GitPaths) == 0 {
		t.Fatalf("config = %+v", config)
	}
}
