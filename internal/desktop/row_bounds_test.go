package desktop

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestFrameClientSnapshotRowBoundsDoNotOverflow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start int64
		count int64
	}{
		{"maximum plus one", math.MaxInt64, 1}, {"maximum minus one plus two", math.MaxInt64 - 1, 2},
		{"large chunk wrap", math.MaxInt64 - 199, 200}, {"minimum", math.MinInt64, 1},
		{"negative", -1, 1}, {"at end", 5, 1}, {"crosses screen end", 4, 2}, {"empty", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			renderer, _ := NewRenderer(&out)
			client, _ := NewFrameClient(renderer)
			generation := "00000000-0000-4000-8000-000000000001"
			snapshot := "00000000-0000-4000-8000-000000000002"
			screen := testScreen()
			if err := client.BeginSnapshot(generation, protocol.SnapshotBeginPayload{SnapshotID: snapshot, Revision: "1", Screen: screen}); err != nil {
				t.Fatal(err)
			}
			err := client.AddSnapshotRows(generation, protocol.SnapshotRowsPayload{SnapshotID: snapshot, Revision: "1", StartRow: tc.start, Rows: testRows(screen.Columns, tc.count)})
			if !errors.Is(err, ErrResyncRequired) || client.assembly != nil || out.Len() != 0 {
				t.Fatalf("invalid chunk result=%v assembly=%v painted=%d", err, client.assembly != nil, out.Len())
			}
			// A rejected transaction does not poison a fresh complete snapshot.
			if err := client.BeginSnapshot(generation, protocol.SnapshotBeginPayload{SnapshotID: snapshot, Revision: "1", Screen: screen}); err != nil {
				t.Fatal(err)
			}
			if err := client.AddSnapshotRows(generation, protocol.SnapshotRowsPayload{SnapshotID: snapshot, Revision: "1", Rows: testRows(screen.Columns, screen.Rows-1)}); err != nil {
				t.Fatal(err)
			}
			if err := client.AddSnapshotRows(generation, protocol.SnapshotRowsPayload{SnapshotID: snapshot, Revision: "1", StartRow: screen.Rows - 1, Rows: testRows(screen.Columns, 1)}); err != nil {
				t.Fatal(err)
			}
			if err := client.EndSnapshot(generation, protocol.SnapshotEndPayload{SnapshotID: snapshot, Revision: "1", RowCount: screen.Rows}); err != nil {
				t.Fatal(err)
			}
			if client.DisplayedRevision() != "1" || out.Len() == 0 {
				t.Fatal("valid recovered boundary snapshot did not display")
			}
		})
	}
}
