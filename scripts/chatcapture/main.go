// Command chatcapture turns one real Claude Code transcript (stdin) into a
// sanitized fixture candidate (stdout) for docs/chat-view.md, question 7.
// It keeps the structure only: every key, the line and block types, tool
// names, flags, numbers and the order of lines. Every other string becomes
// a placeholder with its length; IDs become stable fake IDs (so a tool
// result still pairs with its call, and parentUuid with uuid); the working
// folder becomes /work/demo; times keep their spacing from a fixed start.
// The owner reviews the output before anything is committed.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// keep are keys whose string values are structure, not content.
var keep = map[string]bool{
	"type": true, "subtype": true, "role": true, "name": true, "userType": true, "version": true,
	"stop_reason": true, "stop_sequence": true, "model": true, "level": true, "operation": true,
}

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	idPattern   = regexp.MustCompile(`^(toolu|msg|req|srvtoolu)_[A-Za-z0-9]+$`)
)

type sanitizer struct {
	ids   map[string]string
	start time.Time
	base  time.Time
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "chatcapture:", err)
		os.Exit(1)
	}
}

func run(input io.Reader, output io.Writer) error {
	state := &sanitizer{ids: map[string]string{}, base: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	reader := bufio.NewReaderSize(input, 1<<20)
	writer := bufio.NewWriter(output)
	defer writer.Flush()
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var value any
			if json.Unmarshal(line, &value) != nil {
				fmt.Fprintln(writer, `{"type":"unparsable-line"}`)
			} else {
				encoder := json.NewEncoder(writer)
				encoder.SetEscapeHTML(false)
				_ = encoder.Encode(state.value("", value))
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (state *sanitizer) value(key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for name, item := range typed {
			out[name] = state.value(name, item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = state.value(key, item)
		}
		return out
	case string:
		return state.text(key, typed)
	default:
		return value
	}
}

func (state *sanitizer) text(key, value string) string {
	switch {
	case key == "cwd":
		return "/work/demo"
	case key == "timestamp":
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return "2026-01-01T09:00:00.000Z"
		}
		if state.start.IsZero() {
			state.start = at
		}
		return state.base.Add(at.Sub(state.start)).Format("2006-01-02T15:04:05.000Z")
	case uuidPattern.MatchString(value) || idPattern.MatchString(value):
		if fake, ok := state.ids[value]; ok {
			return fake
		}
		fake := fakeID(value, len(state.ids)+1)
		state.ids[value] = fake
		return fake
	case keep[key] && len(value) <= 64 && !strings.ContainsAny(value, "/\\\n"):
		return value
	default:
		return fmt.Sprintf("<text %d bytes>", len(value))
	}
}

func fakeID(value string, index int) string {
	if uuidPattern.MatchString(value) {
		return fmt.Sprintf("00000000-0000-4000-8000-%012d", index)
	}
	prefix, _, _ := strings.Cut(value, "_")
	return fmt.Sprintf("%s_%024d", prefix, index)
}
