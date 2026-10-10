package main

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestSanitizeKeepsStructureOnly(t *testing.T) {
	input := `{"type":"assistant","uuid":"1b2c3d4e-0000-4000-8000-000000000abc","cwd":"/Users/someone/secret-project","timestamp":"2026-10-10T12:00:05.000Z","message":{"id":"msg_01ABC","content":[{"type":"tool_use","id":"toolu_01XYZ","name":"Bash","input":{"command":"cat ~/.ssh/id_rsa"}}]}}
{"type":"user","parentUuid":"1b2c3d4e-0000-4000-8000-000000000abc","timestamp":"2026-10-10T12:00:07.500Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01XYZ","content":"PRIVATE"}]}}
`
	var output bytes.Buffer
	if err := run(strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, leaked := range []string{"someone", "secret-project", "id_rsa", "PRIVATE", "01ABC", "01XYZ", "1b2c3d4e"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("%q leaked: %s", leaked, text)
		}
	}
	uuid := regexp.MustCompile(`"uuid":"([0-9a-f-]{36})"`).FindStringSubmatch(text)
	if uuid == nil || !strings.Contains(text, `"parentUuid":"`+uuid[1]+`"`) {
		t.Fatalf("parentUuid no longer names its parent: %s", text)
	}
	call := regexp.MustCompile(`"id":"(toolu_\d+)"`).FindStringSubmatch(text)
	if call == nil || !strings.Contains(text, `"tool_use_id":"`+call[1]+`"`) {
		t.Fatalf("the result no longer pairs with its call: %s", text)
	}
	for _, kept := range []string{`"name":"Bash"`, `"type":"tool_result"`, `"cwd":"/work/demo"`, `"2026-01-01T09:00:02.500Z"`,
		`<text 17 bytes>`} {
		if !strings.Contains(text, kept) {
			t.Fatalf("%s missing: %s", kept, text)
		}
	}
}
