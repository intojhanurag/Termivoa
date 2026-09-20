package desktop

import (
	"bytes"
	"errors"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestFrameClientPaintsOnlyCompleteSnapshotAndMatchingPatch(t *testing.T) {
	var output bytes.Buffer
	renderer, err := NewRenderer(&output)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewFrameClient(renderer)
	if err != nil {
		t.Fatal(err)
	}
	generation := "00000000-0000-4000-8000-000000000001"
	snapshotID := "00000000-0000-4000-8000-000000000002"
	screen := testScreen()
	rows := testRows(screen.Columns, screen.Rows)
	if err := client.BeginSnapshot(generation, protocol.SnapshotBeginPayload{SnapshotID: snapshotID, Revision: "9007199254740993", Screen: screen}); err != nil {
		t.Fatal(err)
	}
	if err := client.AddSnapshotRows(generation, protocol.SnapshotRowsPayload{SnapshotID: snapshotID, Revision: "9007199254740993", StartRow: 0, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("partial snapshot was painted: %q", output.String())
	}
	if err := client.EndSnapshot(generation, protocol.SnapshotEndPayload{SnapshotID: snapshotID, Revision: "9007199254740993", RowCount: screen.Rows}); err != nil {
		t.Fatal(err)
	}
	if client.DisplayedRevision() != "9007199254740993" || output.Len() == 0 {
		t.Fatalf("completed snapshot revision/output = %q/%d", client.DisplayedRevision(), output.Len())
	}

	output.Reset()
	rows[0].Runs[0].Cells[0].Text = "x"
	err = client.ApplyPatch(generation, protocol.ScreenPatchPayload{
		BaseRevision: "9007199254740993",
		Revision:     "9007199254740994",
		Screen:       screen,
		Rows:         []protocol.ChangedRow{{Index: 0, Row: rows[0]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.DisplayedRevision() != "9007199254740994" || !bytes.Contains(output.Bytes(), []byte("x")) {
		t.Fatalf("patch revision/output = %q/%q", client.DisplayedRevision(), output.String())
	}
}

func TestFrameClientRejectsInterruptedOrMismatchedState(t *testing.T) {
	var output bytes.Buffer
	renderer, _ := NewRenderer(&output)
	client, _ := NewFrameClient(renderer)
	generation := "00000000-0000-4000-8000-000000000001"
	snapshotID := "00000000-0000-4000-8000-000000000002"
	screen := testScreen()
	if err := client.BeginSnapshot(generation, protocol.SnapshotBeginPayload{SnapshotID: snapshotID, Revision: "1", Screen: screen}); err != nil {
		t.Fatal(err)
	}
	if err := client.AddSnapshotRows(generation, protocol.SnapshotRowsPayload{SnapshotID: snapshotID, Revision: "1", Rows: testRows(screen.Columns, 1)}); err != nil {
		t.Fatal(err)
	}
	err := client.EndSnapshot(generation, protocol.SnapshotEndPayload{SnapshotID: snapshotID, Revision: "1", RowCount: screen.Rows})
	if !errors.Is(err, ErrResyncRequired) || output.Len() != 0 {
		t.Fatalf("interrupted end = %v, output %q", err, output.String())
	}

	err = client.ApplyPatch(generation, protocol.ScreenPatchPayload{BaseRevision: "0", Revision: "1", Screen: screen})
	if !errors.Is(err, ErrResyncRequired) || output.Len() != 0 {
		t.Fatalf("patch without snapshot = %v, output %q", err, output.String())
	}
}

func TestFrameClientCopiesSnapshotRows(t *testing.T) {
	var output bytes.Buffer
	renderer, _ := NewRenderer(&output)
	client, _ := NewFrameClient(renderer)
	generation := "00000000-0000-4000-8000-000000000001"
	snapshotID := "00000000-0000-4000-8000-000000000002"
	screen := testScreen()
	rows := testRows(screen.Columns, screen.Rows)
	if err := client.BeginSnapshot(generation, protocol.SnapshotBeginPayload{SnapshotID: snapshotID, Revision: "1", Screen: screen}); err != nil {
		t.Fatal(err)
	}
	if err := client.AddSnapshotRows(generation, protocol.SnapshotRowsPayload{SnapshotID: snapshotID, Revision: "1", Rows: rows}); err != nil {
		t.Fatal(err)
	}
	rows[0].Runs[0].Cells[0].Text = "mutated"
	if err := client.EndSnapshot(generation, protocol.SnapshotEndPayload{SnapshotID: snapshotID, Revision: "1", RowCount: screen.Rows}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte("mutated")) {
		t.Fatalf("caller mutation reached buffered snapshot: %q", output.String())
	}
}
