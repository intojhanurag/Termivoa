package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

var fuzzSeeds = []string{
	`{"type":"user","uuid":"a","timestamp":"2026-10-10T09:00:00.000Z","message":{"role":"user","content":"hello"}}`,
	`{"type":"assistant","uuid":"b","parentUuid":"a","timestamp":"2026-10-10T09:00:03.000Z","message":{"id":"msg_1","content":[{"type":"thinking","thinking":"x"},{"type":"text","text":"hi ‮ there"},{"type":"tool_use","id":"toolu_1","name":"Edit","input":{"file_path":"/w/a.go","old_string":"a\nb","new_string":"c"}}]}}`,
	`{"type":"user","uuid":"c","timestamp":"2026-10-10T09:00:04.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],"is_error":true}]}}`,
	`{"type":"user","isCompactSummary":true,"uuid":"d","timestamp":"x","message":{"content":"s"}}`,
	`{"type":"summary","summary":"x"}`,
	`not json`,
	`{"type":"assistant","message":{"content":"plain"},"uuid":"e","timestamp":"t"}`,
}

// FuzzClaudeLine: the parser never panics and every block it makes passes
// the wire validator.
func FuzzClaudeLine(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		item := parseClaudeLine(data, 123, "/w")
		for _, block := range item.blocks {
			if err := protocol.ValidateChatBlock(block); err != nil {
				t.Fatalf("%v: %+v", err, block)
			}
		}
		for _, result := range item.results {
			if result.result != nil && len(result.result.Preview) > protocol.MaxChatPreviewBytes {
				t.Fatal("preview over the cap")
			}
		}
	})
}

// FuzzTail: any file content, read through poll and pages, gives valid
// pages and never panics.
func FuzzTail(f *testing.F) {
	f.Add([]byte(strings.Join(fuzzSeeds, "\n")+"\n"), uint8(3))
	f.Add([]byte("{\"type\":\"user\"\n\n\n{}"), uint8(1))
	// One file per fuzz process, rewritten each run: a temp folder per run
	// makes the fuzzer wait on the disk.
	path := filepath.Join(f.TempDir(), "t.jsonl")
	f.Fuzz(func(t *testing.T, data []byte, split uint8) {
		cut := len(data) * int(split) / 256
		if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		info, _ := file.Stat()
		tail := newTranscript(file, Process{PID: 1, Start: time.Now()}, "s", "/w", "t.jsonl", 0, 0, info.Size())
		latest, err := tail.Page(PageRequest{Turns: 3})
		if err != nil {
			t.Fatal(err)
		}
		check(t, latest)
		appended, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		_, _ = appended.Write(data[cut:])
		_ = appended.Close()
		if _, err := tail.Poll(); err != nil {
			t.Fatal(err)
		}
		if after, err := tail.Page(PageRequest{After: latest.After, Turns: 3}); err == nil {
			check(t, after)
		}
		if latest.Before != nil {
			if before, err := tail.Page(PageRequest{Before: *latest.Before, Turns: 3}); err == nil {
				check(t, before)
			}
		}
	})
}

func check(t *testing.T, page protocol.ChatPagePayload) {
	t.Helper()
	session, generation := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	if _, err := protocol.EncodeHost(protocol.Envelope{Version: 1, Type: protocol.ChatPage,
		RequestID: "33333333-3333-4333-8333-333333333333", SessionID: &session, Generation: &generation}, page); err != nil {
		data, _ := json.Marshal(page)
		t.Fatalf("%v: %.400s", err, data)
	}
}

func TestParseDropsResultsWithInvalidIDs(t *testing.T) {
	item := parseClaudeLine([]byte(`{"type":"user","uuid":"u","timestamp":"2026-10-10T10:00:00Z","message":{"role":"user","content":[`+
		`{"type":"tool_result","tool_use_id":"toolu_1/../x","content":"a"},{"type":"tool_result","tool_use_id":"toolu_ok","content":"b"}]}}`), 0, "/w")
	if item.kind != kindResults || len(item.results) != 1 || item.results[0].id != "toolu_ok" {
		t.Fatalf("item = %+v", item)
	}
}
