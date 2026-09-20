package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestSplitInputMatchesWholeInput(t *testing.T) {
	t.Parallel()
	fixtures := map[string][]byte{
		"ansi-and-cjk": []byte("A界\x1b[2;4H\x1b[31mred\x1b[m"),
		"combining":    []byte("e\u0301"),
		"variation":    []byte("☕️"),
		"zwj":          []byte("👩‍💻"),
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			want := snapshotAfterWrites(t, fixture)
			want.Revision = 0
			for split := 1; split < len(fixture); split++ {
				t.Run(fmt.Sprintf("split-%d", split), func(t *testing.T) {
					got := snapshotAfterWrites(t, fixture[:split], fixture[split:])
					got.Revision = 0
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("frames differ: got cursor=%+v rows=%q/%q, want cursor=%+v rows=%q/%q",
							got.Cursor, rowText(got.Screen[0]), rowText(got.Screen[1]),
							want.Cursor, rowText(want.Screen[0]), rowText(want.Screen[1]))
					}
				})
			}
		})
	}
}

func TestCombiningClusterIsIndependentOfWriteBoundaries(t *testing.T) {
	t.Parallel()
	whole := snapshotAfterWrites(t, []byte("e\u0301"))
	split := snapshotAfterWrites(t, []byte("e"), []byte("\u0301"))
	whole.Revision = 0
	split.Revision = 0
	if !reflect.DeepEqual(whole, split) {
		t.Fatalf("canonical state depends on PTY read boundary: whole=%#v split=%#v",
			whole.Screen[0].Cells[:2], split.Screen[0].Cells[:2])
	}
}

func TestSnapshotDoesNotCreateGraphemeBoundary(t *testing.T) {
	t.Parallel()
	for name, parts := range map[string][2]string{
		"combining": {"e", "\u0301"},
		"variation": {"☕", "️"},
		"zwj":       {"👩", "‍💻"},
	} {
		t.Run(name, func(t *testing.T) {
			want := snapshotAfterWrites(t, []byte(parts[0]+parts[1]))
			want.Revision = 0

			core := newTestCore(t, 20, 5)
			mustWrite(t, core, parts[0])
			if _, err := core.Snapshot(context.Background()); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, core, parts[1])
			got, err := core.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got.Revision = 0
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("snapshot changed grapheme state: got=%#v want=%#v",
					got.Screen[0].Cells[:3], want.Screen[0].Cells[:3])
			}
		})
	}
}

func TestWidthMethodTracksUnicodeCoreMode(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	frame, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if frame.WidthMethod != "wcwidth" {
		t.Fatalf("default width method = %q", frame.WidthMethod)
	}
	mustWrite(t, core, "\x1b[?2027h")
	frame, err = core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if frame.WidthMethod != "unicode-graphemes" {
		t.Fatalf("unicode-core width method = %q", frame.WidthMethod)
	}
}

func TestStoredCellWidthMatchesAdvertisedMethod(t *testing.T) {
	t.Parallel()
	const cluster = "🏳️‍🌈"

	for _, test := range []struct {
		name   string
		prefix string
		method ansi.Method
	}{
		{name: "wcwidth", method: ansi.WcWidth},
		{name: "unicode-graphemes", prefix: "\x1b[?2027h", method: ansi.GraphemeWidth},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := snapshotAfterWrites(t, []byte(test.prefix+cluster))
			got := frame.Screen[0].Cells[0].Width
			want := test.method.StringWidth(cluster)
			if got != want {
				t.Fatalf("stored width = %d, advertised method width = %d", got, want)
			}
		})
	}
}

func TestWideContinuationDoesNotMarkBlankCells(t *testing.T) {
	t.Parallel()
	frame := snapshotAfterWrites(t, []byte("界"))
	if frame.Screen[0].Cells[0].Width != 2 || frame.Screen[0].Cells[0].Continuation {
		t.Fatalf("wide leading cell = %#v", frame.Screen[0].Cells[0])
	}
	if !frame.Screen[0].Cells[1].Continuation {
		t.Fatalf("wide continuation = %#v", frame.Screen[0].Cells[1])
	}
	if frame.Screen[0].Cells[2].Continuation || frame.Screen[0].Cells[2].Text != " " {
		t.Fatalf("ordinary blank cell = %#v", frame.Screen[0].Cells[2])
	}
}

func TestQueryHasOneReplyWithoutViewers(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	if err := core.WriteOutput(ctx, []byte("\x1b[3;7H\x1b[6n")); err != nil {
		t.Fatal(err)
	}
	if got := readOutput(t, core.Output()); got != "\x1b[3;7R" {
		t.Fatalf("query reply = %q, want one CPR", got)
	}
	assertNoOutput(t, core.Output())
}

func TestOperatingStatusQueryUsesANSIReplyOnce(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	if err := core.WriteOutput(ctx, []byte("\x1b[5n")); err != nil {
		t.Fatal(err)
	}
	if got := readOutput(t, core.Output()); got != "\x1b[0n" {
		t.Fatalf("operating status reply = %q, want one ANSI ready response", got)
	}
	assertNoOutput(t, core.Output())
}

func TestUserInputAndRepliesShareOrderedOutput(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	if err := core.SendText(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := readOutput(t, core.Output()); got != "hello" {
		t.Fatalf("text output = %q", got)
	}
	if err := core.WriteOutput(ctx, []byte("\x1b[?2004h")); err != nil {
		t.Fatal(err)
	}
	if err := core.Paste(ctx, "one\ntwo"); err != nil {
		t.Fatal(err)
	}
	wantPaste := "\x1b[200~one\ntwo\x1b[201~"
	if got := collectOutput(t, core.Output(), len(wantPaste)); got != wantPaste {
		t.Fatalf("paste output = %q", got)
	}
	if err := core.SendKey(ctx, uv.KeyPressEvent(uv.Key{Code: uv.KeyEnter})); err != nil {
		t.Fatal(err)
	}
	if got := readOutput(t, core.Output()); got != "\r" {
		t.Fatalf("enter output = %q", got)
	}
}

func TestAlternateScreenPreservesNormalState(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	mustWrite(t, core, "normal")
	mustWrite(t, core, "\x1b[?1049halt")
	alternate, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !alternate.Alternate || rowText(alternate.Screen[0]) != "alt" {
		t.Fatalf("alternate frame = alt:%v row:%q", alternate.Alternate, rowText(alternate.Screen[0]))
	}

	mustWrite(t, core, "\x1b[?1049l")
	normal, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if normal.Alternate || rowText(normal.Screen[0]) != "normal" {
		t.Fatalf("normal frame = alt:%v row:%q", normal.Alternate, rowText(normal.Screen[0]))
	}
}

func TestCursorStyleRestoresWithSavedAndAlternateBuffers(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	mustWrite(t, core, "\x1b[4 q\x1b7\x1b[5 q\x1b8")
	frame, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Cursor.Style != CursorUnderline || frame.Cursor.Blink {
		t.Fatalf("restored cursor = %+v, want steady underline", frame.Cursor)
	}

	mustWrite(t, core, "\x1b[?1049h\x1b[5 q\x1b[?1049l")
	frame, err = core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Cursor.Style != CursorUnderline || frame.Cursor.Blink {
		t.Fatalf("normal-buffer cursor = %+v, want steady underline", frame.Cursor)
	}
}

func TestRightAndBottomBoundaryAutowrap(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	mustWrite(t, core, "\x1b[5;20HA")
	beforeWrap, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if beforeWrap.Cursor.Column != 19 || beforeWrap.Cursor.Row != 4 {
		t.Fatalf("pending-wrap cursor = %+v", beforeWrap.Cursor)
	}

	mustWrite(t, core, "B")
	afterWrap, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if afterWrap.Cursor.Column != 1 || afterWrap.Cursor.Row != 4 {
		t.Fatalf("post-scroll cursor = %+v", afterWrap.Cursor)
	}
	if got := afterWrap.Screen[3].Cells[19].Text; got != "A" {
		t.Fatalf("bottom-right cell after scroll = %q, want A", got)
	}
	if got := afterWrap.Screen[4].Cells[0].Text; got != "B" {
		t.Fatalf("wrapped cell = %q, want B", got)
	}
}

func TestScrollingMargins(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	mustWrite(t, core, "\x1b[1;1Hone\x1b[2;1Htwo\x1b[3;1Hthree\x1b[4;1Hfour\x1b[5;1Hfive")
	mustWrite(t, core, "\x1b[2;4r\x1b[4;1H\n")
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "three", "four", "", "five"}
	for row, text := range want {
		if got := rowText(frame.Screen[row]); got != text {
			t.Fatalf("row %d = %q, want %q", row+1, got, text)
		}
	}
}

func TestInsertDeleteEraseAndStyles(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	mustWrite(t, core, "abcdef\x1b[1;3H\x1b[2@")
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rowText(frame.Screen[0]); got != "ab  cdef" {
		t.Fatalf("inserted cells row = %q", got)
	}

	mustWrite(t, core, "\x1b[1;3H\x1b[2P")
	frame, err = core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rowText(frame.Screen[0]); got != "abcdef" {
		t.Fatalf("deleted cells row = %q", got)
	}

	mustWrite(t, core, "\x1b[1;3H\x1b[2X")
	frame, err = core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rowText(frame.Screen[0]); got != "ab  ef" {
		t.Fatalf("erased cells row = %q", got)
	}

	mustWrite(t, core, "\x1b[2;1H\x1b[31;44;1;4;7mX\x1b[m")
	frame, err = core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cell := frame.Screen[1].Cells[0]
	if !cell.Bold || !cell.Reverse || cell.Underline == 0 {
		t.Fatalf("styled cell attributes = %#v", cell)
	}
	if cell.Foreground == frame.Foreground || cell.Background == frame.Background {
		t.Fatalf("styled cell colors were not resolved: %#v", cell)
	}

	mustWrite(t, core, "\x1b[2K")
	frame, err = core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := rowText(frame.Screen[1]); got != "" {
		t.Fatalf("erased line = %q", got)
	}
}

func TestAlternateBufferSurvivesSnapshotAndResize(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	mustWrite(t, core, "normal")
	if _, err := core.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, core, "\x1b[?1049hfull-screen")
	if _, err := core.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := core.Resize(context.Background(), 30, 6); err != nil {
		t.Fatal(err)
	}
	alternate, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !alternate.Alternate || alternate.Columns != 30 || alternate.Rows != 6 || rowText(alternate.Screen[0]) != "full-screen" {
		t.Fatalf("resized alternate frame = alt:%v size:%dx%d row:%q",
			alternate.Alternate, alternate.Columns, alternate.Rows, rowText(alternate.Screen[0]))
	}

	mustWrite(t, core, "\x1b[?1049l")
	normal, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if normal.Alternate || normal.Columns != 30 || normal.Rows != 6 || rowText(normal.Screen[0]) != "normal" {
		t.Fatalf("restored normal frame = alt:%v size:%dx%d row:%q",
			normal.Alternate, normal.Columns, normal.Rows, rowText(normal.Screen[0]))
	}
}

func TestHyperlinkMetadataIsBoundedAndSanitized(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	params := strings.Repeat("p", MaxLinkBytes*2)
	mustWrite(t, core, "\x1b]8;"+params+";https://example.com\x1b\\linked\x1b]8;;\x1b\\")
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.Screen[0].Cells[0].Link; got != "https://example.com" {
		t.Fatalf("sanitized link = %q", got)
	}

	core = newTestCore(t, 20, 5)
	oversizedURL := "https://example.com/" + strings.Repeat("x", MaxLinkBytes)
	mustWrite(t, core, "\x1b]8;;"+oversizedURL+"\x1b\\x\x1b]8;;\x1b\\")
	frame, err = core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := frame.Screen[0].Cells[0].Link; got != "" {
		t.Fatalf("oversized link was retained: %d bytes", len(got))
	}
}

func TestResizeAndBounds(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	if err := core.Resize(ctx, 40, 10); err != nil {
		t.Fatal(err)
	}
	frame, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Columns != 40 || frame.Rows != 10 || len(frame.Screen) != 10 || len(frame.Screen[0].Cells) != 40 {
		t.Fatalf("unexpected resized frame: %dx%d", frame.Columns, frame.Rows)
	}
	if err := core.Resize(ctx, 19, 5); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("resize below bound = %v", err)
	}
	if err := core.Resize(ctx, 400, 201); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("resize above bound = %v", err)
	}
}

func TestInputAndCellContentBounds(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	if err := core.SendText(ctx, "line\n"); !errors.Is(err, ErrInvalidText) {
		t.Fatalf("text control validation = %v", err)
	}
	if err := core.Paste(ctx, "\x1b[31m"); !errors.Is(err, ErrInvalidPaste) {
		t.Fatalf("paste escape validation = %v", err)
	}
	if err := core.Paste(ctx, strings.Repeat("x", MaxPasteBytes+1)); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("paste size validation = %v", err)
	}

	cluster := "e" + strings.Repeat("\u0301", MaxCellContentBytes)
	mustWrite(t, core, cluster)
	frame, err := core.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range frame.Screen[0].Cells {
		if len(cell.Text) > MaxCellContentBytes {
			t.Fatalf("retained cell has %d bytes, limit %d", len(cell.Text), MaxCellContentBytes)
		}
	}
}

func TestHistoryPayloadIsByteBounded(t *testing.T) {
	t.Parallel()
	const historyBudget = 2500
	core, err := New(context.Background(), Config{
		Columns:      20,
		Rows:         5,
		HistoryBytes: historyBudget,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })

	for line := range 50 {
		mustWrite(t, core, fmt.Sprintf("line-%02d\r\n", line))
	}
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.HistoryBytes > historyBudget {
		t.Fatalf("history payload = %d bytes, limit %d", frame.HistoryBytes, historyBudget)
	}
	if len(frame.History) == 0 {
		t.Fatal("history byte limit evicted every row")
	}
}

func TestSnapshotIsImmutable(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	mustWrite(t, core, "before")
	before, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, core, "after")
	if got := rowText(before.Screen[0]); got != "before" {
		t.Fatalf("captured frame mutated: %q", got)
	}
}

func TestConcurrentUseAndShutdown(t *testing.T) {
	core := newTestCore(t, 20, 5)
	ctx := context.Background()
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := range 100 {
				_ = core.WriteOutput(ctx, []byte{byte('a' + worker%26), byte('0' + iteration%10)})
				_, _ = core.Snapshot(ctx)
			}
		}()
	}
	workers.Wait()
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if err := core.WriteOutput(ctx, []byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close = %v", err)
	}
	for range core.Output() {
	}
}

func TestShutdownWithStalledOutputConsumer(t *testing.T) {
	core, err := New(context.Background(), Config{
		Columns:     20,
		Rows:        5,
		OutputQueue: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := range 3 {
		result := make(chan error, 1)
		go func() { result <- core.SendText(context.Background(), fmt.Sprintf("input-%d", i)) }()
		if i < 2 {
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("terminal input blocked before the output queue stalled")
			}
		}
	}

	closed := make(chan error, 1)
	go func() { closed <- core.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal close deadlocked behind a stalled output consumer")
	}
}

func newTestCore(t *testing.T, columns, rows int) *Core {
	t.Helper()
	core, err := New(context.Background(), Config{Columns: columns, Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	return core
}

func snapshotAfterWrites(t *testing.T, writes ...[]byte) Frame {
	t.Helper()
	core := newTestCore(t, 20, 5)
	for _, write := range writes {
		if err := core.WriteOutput(context.Background(), write); err != nil {
			t.Fatal(err)
		}
	}
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func mustWrite(t *testing.T, core *Core, value string) {
	t.Helper()
	if err := core.WriteOutput(context.Background(), []byte(value)); err != nil {
		t.Fatal(err)
	}
}

func rowText(row Row) string {
	var builder strings.Builder
	for _, cell := range row.Cells {
		if !cell.Continuation {
			builder.WriteString(cell.Text)
		}
	}
	return strings.TrimRight(builder.String(), " ")
}

func readOutput(t *testing.T, output <-chan []byte) string {
	t.Helper()
	select {
	case value, ok := <-output:
		if !ok {
			t.Fatal("terminal output closed unexpectedly")
		}
		return string(value)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal output")
		return ""
	}
}

func collectOutput(t *testing.T, output <-chan []byte, size int) string {
	t.Helper()
	var buffer bytes.Buffer
	deadline := time.After(time.Second)
	for buffer.Len() < size {
		select {
		case value, ok := <-output:
			if !ok {
				t.Fatal("terminal output closed unexpectedly")
			}
			buffer.Write(value)
		case <-deadline:
			t.Fatalf("timed out after %d of %d output bytes", buffer.Len(), size)
		}
	}
	return buffer.String()
}

func assertNoOutput(t *testing.T, output <-chan []byte) {
	t.Helper()
	select {
	case value := <-output:
		t.Fatalf("unexpected extra output %q", value)
	case <-time.After(25 * time.Millisecond):
	}
}
