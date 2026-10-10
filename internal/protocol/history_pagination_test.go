package protocol

import (
	"context"
	"strings"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

// The client must follow the byte-limited cursor, rather than assume that
// 25 pages of up to 200 rows cover the retained history. Use a real core to
// ensure the reproduction also fits the normalized 32 MiB retention limit.
func TestStyledRetainedHistoryRequiresMoreThan25Pages(t *testing.T) {
	ctx := context.Background()
	core, err := terminal.New(ctx, terminal.Config{Columns: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	go func() {
		for range core.Output() {
		}
	}()
	line := strings.Repeat("\x1b[31mx\x1b[32mx", 40) + "\r\n"
	for range 2000 {
		if err := core.WriteOutput(ctx, []byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	frame, err := core.ScreenSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	offset, pages, retained := 0, 0, 0
	for {
		result, err := core.ReadHistory(ctx, terminal.HistoryReadRequest{
			Epoch: frame.HistoryEpoch, Revision: frame.HistoryRevision, Offset: offset, Limit: terminal.MaxHistoryPageRows,
		})
		if err != nil {
			t.Fatal(err)
		}
		retained = result.RetainedRows
		page, err := HistoryPageFrom(result)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		offset += len(page.Rows)
		if pages == 25 && page.Complete {
			t.Fatal("styled fixture no longer reproduces byte-limited pagination")
		}
		if page.Complete {
			break
		}
		if page.NextOffset == nil || *page.NextOffset != int64(offset) {
			t.Fatalf("invalid cursor: %#v", page.NextOffset)
		}
	}
	if pages <= 25 || offset != retained {
		t.Fatalf("pages=%d loaded=%d retained=%d", pages, offset, retained)
	}
	t.Logf("loaded all %d retained rows in %d byte-limited pages", retained, pages)
}
