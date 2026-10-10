//go:build darwin || linux

package transcript_test

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/transcript"
	"github.com/intojhanurag/phone-terminal/internal/transcript/transcripttest"
)

// sampleTurns writes count typical turns (with a 4 KiB tool output each)
// and returns the file's bytes.
func sampleTurns(b *testing.B, count int) (*fixture, []byte) {
	b.Helper()
	f := newFixture(b)
	for range count {
		f.session.Turn("Please make the tests pass and explain the change")
		call := transcripttest.ToolID()
		f.session.Assistant(transcripttest.MessageID(), transcripttest.ToolUse(call, "Read", map[string]any{"file_path": f.cwd + "/main.go"}))
		f.session.Result(call, strings.Repeat("func main() { fmt.Println(\"hello\") }\n", 110), false)
	}
	data, err := os.ReadFile(f.session.Path)
	if err != nil {
		b.Fatal(err)
	}
	return f, data
}

// BenchmarkClaudeParse is parse throughput (budget: 50 MB/s per core):
// Poll reads and parses every line of a fresh transcript.
func BenchmarkClaudeParse(b *testing.B) {
	_, data := sampleTurns(b, 400)
	lines := bytes.Count(data, []byte{'\n'})
	b.SetBytes(int64(len(data)))
	b.ReportMetric(float64(lines), "lines/op")
	for b.Loop() {
		transcript.ParseAll(data)
	}
}

// BenchmarkBackfill is the first page of a 200 MB synthetic transcript
// (budget: 300 ms, at most 8 MiB read).
func BenchmarkBackfill(b *testing.B) {
	f, data := sampleTurns(b, 400)
	file, err := os.OpenFile(f.session.Path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		b.Fatal(err)
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	for written := len(data); written < 200<<20; written += len(data) {
		_, _ = writer.Write(data)
	}
	_ = writer.Flush()
	_ = file.Close()
	tail := transcript.Bind(b.Context(), f.config, f.procs).Transcript
	if tail == nil {
		b.Fatal("not linked")
	}
	defer tail.Close()
	info, _ := os.Stat(filepath.Clean(f.session.Path))
	b.ReportMetric(float64(info.Size())/(1<<20), "MiB-file")
	b.ResetTimer()
	start := tail.BytesRead()
	for b.Loop() {
		if _, err := tail.Page(transcript.PageRequest{Turns: 60}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(tail.BytesRead()-start)/float64(b.N)/(1<<20), "MiB-read/op")
}
