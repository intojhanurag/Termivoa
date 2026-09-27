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

func TestWideContinuationInheritsLeadingCellStyle(t *testing.T) {
	t.Parallel()
	frame := snapshotAfterWrites(t, []byte("\x1b[1;4;31;44m界"))
	leading := frame.Screen[0].Cells[0]
	continuation := frame.Screen[0].Cells[1]

	if !continuation.Continuation || continuation.Width != 0 || continuation.Text != "" {
		t.Fatalf("wide continuation shape = %#v", continuation)
	}
	leading.Text = ""
	leading.Width = 0
	leading.Continuation = true
	leading.Link = ""
	if !reflect.DeepEqual(continuation, leading) {
		t.Fatalf("wide continuation style = %#v, want inherited %#v", continuation, leading)
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

func TestStructuredMouseUsesAuthoritativeTerminalMode(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	ctx := context.Background()

	if err := core.WriteOutput(ctx, []byte("\x1b[?1000h\x1b[?1006h")); err != nil {
		t.Fatal(err)
	}
	if err := core.SendMouse(ctx, uv.MouseClickEvent(uv.Mouse{X: 1, Y: 2, Button: uv.MouseLeft})); err != nil {
		t.Fatal(err)
	}
	if got := readOutput(t, core.Output()); got != "\x1b[<0;2;3M" {
		t.Fatalf("mouse output = %q", got)
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
	const historyBudget = 12000
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
	if frame.HistoryRows == 0 {
		t.Fatal("history byte limit evicted every row")
	}
	page, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Limit: MaxHistoryPageRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != frame.HistoryRows {
		t.Fatalf("paged rows = %d, retained metadata = %d", len(page.Rows), frame.HistoryRows)
	}
}

func TestSnapshotsExposeHistoryMetadataWithoutRows(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	for line := range 20 {
		mustWrite(t, core, fmt.Sprintf("screen-only-%02d\r\n", line))
	}
	full, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if full.HistoryRows == 0 || full.HistoryBytes == 0 {
		t.Fatal("fixture did not produce retained history")
	}
	live, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if live.HistoryRows != full.HistoryRows || live.HistoryBytes != full.HistoryBytes ||
		live.HistoryEpoch != full.HistoryEpoch || live.HistoryRevision != full.HistoryRevision {
		t.Fatalf("screen-only history metadata = rows %d token %d/%d, want rows %d token %d/%d",
			live.HistoryRows, live.HistoryEpoch, live.HistoryRevision,
			full.HistoryRows, full.HistoryEpoch, full.HistoryRevision)
	}
	if live.Revision != full.Revision || len(live.Screen) != live.Rows {
		t.Fatalf("screen-only snapshot lost live state: revision=%d/%d rows=%d/%d", live.Revision, full.Revision, len(live.Screen), live.Rows)
	}
}

func TestHistoryPagesAreImmutableAndBounded(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	writeNumberedHistory(t, core, 210)
	frame, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	first, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Limit: MaxHistoryPageRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Changed || first.Complete || len(first.Rows) != MaxHistoryPageRows ||
		first.NextOffset == nil || *first.NextOffset != MaxHistoryPageRows {
		t.Fatalf("first history page = %#v", first)
	}
	original := rowText(first.Rows[0])
	first.Rows[0].Cells[0].Text = "mutated by caller"
	again, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rowText(again.Rows[0]); got != original {
		t.Fatalf("actor-owned page was mutable: got %q, want %q", got, original)
	}

	second, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision,
		Offset: *first.NextOffset, Limit: MaxHistoryPageRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Complete || second.NextOffset != nil ||
		len(first.Rows)+len(second.Rows) != frame.HistoryRows {
		t.Fatalf("second history page = %#v; retained rows = %d", second, frame.HistoryRows)
	}
}

func TestHistoryReadDetectsConcurrentChange(t *testing.T) {
	core := newTestCore(t, 20, 5)
	writeNumberedHistory(t, core, 30)
	frame, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Limit: 10,
	})
	if err != nil || first.Changed {
		t.Fatalf("initial page = %#v, %v", first, err)
	}

	changed := make(chan error, 1)
	go func() { changed <- core.WriteOutput(context.Background(), []byte("concurrent-change\r\n")) }()
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	stale, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Offset: 10, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Changed || len(stale.Rows) != 0 || stale.Revision == frame.HistoryRevision {
		t.Fatalf("stale history read = %#v", stale)
	}
}

func TestHistoryResetAndAlternateScreenIsolation(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	writeNumberedHistory(t, core, 20)
	before, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, core, "\x1b[?1049h"+strings.Repeat("alternate\r\n", 40)+"\x1b[3J")
	alternate, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !alternate.Alternate || alternate.HistoryRows != before.HistoryRows ||
		alternate.HistoryEpoch != before.HistoryEpoch || alternate.HistoryRevision != before.HistoryRevision {
		t.Fatalf("alternate screen contaminated normal history: before=%#v alternate=%#v", before, alternate)
	}

	mustWrite(t, core, "\x1b[?1049l\x1b[3J")
	after, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Alternate || after.HistoryRows != 0 || after.HistoryEpoch != before.HistoryEpoch+1 || after.HistoryRevision != 0 {
		t.Fatalf("normal history reset metadata = rows %d token %d/%d", after.HistoryRows, after.HistoryEpoch, after.HistoryRevision)
	}
	stale, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: before.HistoryEpoch, Revision: before.HistoryRevision, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Changed || stale.Epoch != after.HistoryEpoch || stale.Revision != after.HistoryRevision {
		t.Fatalf("reset did not invalidate frozen token: %#v", stale)
	}
}

func TestHistoryTokenChangesOnlyWhenResizeChangesPageShape(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	writeNumberedHistory(t, core, 20)
	before, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := core.Resize(context.Background(), 20, 6); err != nil {
		t.Fatal(err)
	}
	heightOnly, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if heightOnly.HistoryEpoch != before.HistoryEpoch || heightOnly.HistoryRevision != before.HistoryRevision {
		t.Fatalf("height-only resize invalidated history token: %d/%d -> %d/%d",
			before.HistoryEpoch, before.HistoryRevision,
			heightOnly.HistoryEpoch, heightOnly.HistoryRevision)
	}
	if err := core.Resize(context.Background(), 21, 6); err != nil {
		t.Fatal(err)
	}
	widthChanged, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if widthChanged.HistoryRevision == heightOnly.HistoryRevision {
		t.Fatalf("width resize kept history token %d/%d", widthChanged.HistoryEpoch, widthChanged.HistoryRevision)
	}
}

func TestHistoryRetentionBoundsEmulatorRowsAndNormalizedBytes(t *testing.T) {
	t.Parallel()
	const historyBudget = 12000
	core, err := New(context.Background(), Config{
		Columns: 20, Rows: 5, ScrollbackRows: MaxHistoryRows, HistoryBytes: historyBudget,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	writeNumberedHistory(t, core, MaxHistoryRows+100)
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.HistoryRows > MaxHistoryRows || frame.HistoryBytes > historyBudget {
		t.Fatalf("retained history = rows %d bytes %d; limits %d/%d",
			frame.HistoryRows, frame.HistoryBytes, MaxHistoryRows, historyBudget)
	}
	if frame.HistoryRows == 0 {
		t.Fatal("small-width fixture unexpectedly evicted every row")
	}

	if err := core.Resize(context.Background(), MaxColumns, 5); err != nil {
		t.Fatal(err)
	}
	wide, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if wide.HistoryRows != 0 {
		t.Fatalf("emulator kept %d hidden rows after normalized width exceeded byte budget", wide.HistoryRows)
	}
	page, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: wide.HistoryEpoch, Revision: wide.HistoryRevision, Limit: MaxHistoryPageRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Complete || len(page.Rows) != 0 || page.RetainedBytes != 0 {
		t.Fatalf("empty bounded page = %#v", page)
	}
}

func TestHistoryRowLimitEvictsOldestCompleteRows(t *testing.T) {
	t.Parallel()
	core, err := New(context.Background(), Config{
		Columns: 20, Rows: 5, ScrollbackRows: 3, HistoryBytes: MaxHistoryBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	writeNumberedHistory(t, core, 20)
	frame, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.HistoryRows != 3 {
		t.Fatalf("retained rows = %d, want 3", frame.HistoryRows)
	}
	page, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 3 {
		t.Fatalf("paged retained rows = %d, want 3", len(page.Rows))
	}
	if got := rowText(page.Rows[0]); got == "line-0000" || got == "" {
		t.Fatalf("oldest complete rows were not evicted: first retained %q", got)
	}
}

func TestHistoryNeverRetainsMoreThanSessionMaximum(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	writeNumberedHistory(t, core, MaxHistoryRows+100)
	frame, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.HistoryRows != MaxHistoryRows {
		t.Fatalf("retained rows = %d, want maximum %d", frame.HistoryRows, MaxHistoryRows)
	}
	page, err := core.ReadHistory(context.Background(), HistoryReadRequest{
		Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision,
		Offset: MaxHistoryRows - 1, Limit: MaxHistoryPageRows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Complete || len(page.Rows) != 1 || page.NextOffset != nil {
		t.Fatalf("maximum boundary page = %#v", page)
	}
}

func TestHistoryReadRejectsBoundaryViolations(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	for _, request := range []HistoryReadRequest{
		{Offset: -1, Limit: 1},
		{Offset: MaxHistoryRows, Limit: 1},
		{Offset: 0, Limit: 0},
		{Offset: 0, Limit: MaxHistoryPageRows + 1},
	} {
		if _, err := core.ReadHistory(context.Background(), request); !errors.Is(err, ErrInvalidHistoryRequest) {
			t.Fatalf("ReadHistory(%#v) error = %v", request, err)
		}
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

func TestCancellationAfterEnqueueWaitsForAuthoritativeResult(t *testing.T) {
	commands := make(chan command)
	done := make(chan struct{})
	core := &Core{commands: commands, done: done}
	received := make(chan command, 1)
	release := make(chan struct{})
	go func() {
		cmd := <-commands
		received <- cmd
		<-release
		cmd.reply <- result{}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		returned <- core.execute(ctx, command{kind: commandText, text: "do not duplicate"})
	}()

	<-received
	cancel()
	select {
	case err := <-returned:
		t.Fatalf("accepted command returned before actor result: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("accepted command result = %v, want success", err)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted command did not return the actor result")
	}
}

func TestOSCTitleWithC1ContinuationBytesDoesNotLeak(t *testing.T) {
	t.Parallel()
	// U+201C/U+201D (E2 80 9C/9D) and U+2733 (E2 9C B3) contain C1 bytes.
	for name, input := range map[string]string{
		"quotes":      "abc\r\n\x1b]0;say \xe2\x80\x9chi\xe2\x80\x9d\x07def\r\n",
		"claude-code": "abc\r\n\x1b]0;\xe2\x9c\xb3 Claude Code\x07def\r\n",
		"esc-st":      "abc\r\n\x1b]2;\xe2\x9c\xb3 x\x1b\\def\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			for split := range len(input) {
				frame := snapshotAfterWrites(t, []byte(input[:split]), []byte(input[split:]))
				got := []string{rowText(frame.Screen[0]), rowText(frame.Screen[1]), rowText(frame.Screen[2])}
				if want := []string{"abc", "def", ""}; !reflect.DeepEqual(got, want) {
					t.Fatalf("split %d: screen rows = %q, want %q", split, got, want)
				}
			}
		})
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

func writeNumberedHistory(t *testing.T, core *Core, lines int) {
	t.Helper()
	var output strings.Builder
	for line := range lines {
		fmt.Fprintf(&output, "line-%04d\r\n", line)
	}
	mustWrite(t, core, output.String())
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
