package ptyhost

import "testing"

func TestParseLsofDirectory(t *testing.T) {
	for name, test := range map[string]struct{ output, want string }{
		"cwd":         {"p123\nfcwd\nn/Users/me/src app\n", "/Users/me/src app"},
		"no newline":  {"p1\nfcwd\nn/tmp", "/tmp"},
		"other fd":    {"p1\nftxt\nn/bin/zsh\nfcwd\nn/work\n", "/work"},
		"empty":       {"", ""},
		"no cwd file": {"p1\nftxt\nn/bin/zsh\n", ""},
		"name first":  {"n/etc\np1\n", ""},
	} {
		if got := parseLsofDirectory([]byte(test.output)); got != test.want {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
	}
}
