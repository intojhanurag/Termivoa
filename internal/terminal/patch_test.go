package terminal

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSnapshotPlusPatchEqualsCanonicalFrame(t *testing.T) {
	t.Parallel()
	base := snapshotAfterWrites(t, []byte("normal"))
	next := snapshotAfterWrites(t, []byte("normal\x1b[2;1H\x1b[31mchanged\x1b[m"))
	next.Revision = base.Revision + 1

	patch, err := Diff(base, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.ChangedRows) != 1 || patch.ChangedRows[0].Index != 1 {
		t.Fatalf("changed rows = %#v, want row 1 only", patch.ChangedRows)
	}

	got, err := ApplyPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, next) {
		t.Fatalf("patched frame differs from canonical frame\ngot:  %#v\nwant: %#v", got, next)
	}
}

func TestPatchCarriesMetadataWithoutChangedRows(t *testing.T) {
	t.Parallel()
	base := snapshotAfterWrites(t, []byte("same"))
	next := cloneFrame(base)
	next.Revision++
	next.Cursor.Visible = !base.Cursor.Visible
	next.Alternate = !base.Alternate

	patch, err := Diff(base, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.ChangedRows) != 0 {
		t.Fatalf("changed rows = %d, want none", len(patch.ChangedRows))
	}
	got, err := ApplyPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, next) {
		t.Fatalf("metadata-only patch = %#v, want %#v", got, next)
	}
}

func TestPatchRoundTripPreservesHistoryChanges(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	writeNumberedHistory(t, core, 8)
	base, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, core, "appended-history\r\n")
	appended, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if appended.HistoryRows <= base.HistoryRows || appended.HistoryBytes <= base.HistoryBytes ||
		appended.HistoryRevision == base.HistoryRevision {
		t.Fatalf("history did not advance: base rows/bytes/token = %d/%d/%d:%d, next = %d/%d/%d:%d",
			base.HistoryRows, base.HistoryBytes, base.HistoryEpoch, base.HistoryRevision,
			appended.HistoryRows, appended.HistoryBytes, appended.HistoryEpoch, appended.HistoryRevision)
	}
	assertPatchRoundTrip(t, base, appended)

	mustWrite(t, core, "\x1b[3J")
	reset, err := core.ScreenSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reset.HistoryRows != 0 || reset.HistoryBytes != 0 ||
		reset.HistoryEpoch == appended.HistoryEpoch || reset.HistoryRevision != 0 {
		t.Fatalf("history reset metadata = rows/bytes/token %d/%d/%d:%d",
			reset.HistoryRows, reset.HistoryBytes, reset.HistoryEpoch, reset.HistoryRevision)
	}
	assertPatchRoundTrip(t, appended, reset)
}

func assertPatchRoundTrip(t *testing.T, base, next Frame) Patch {
	t.Helper()
	patch, err := Diff(base, next)
	if err != nil {
		t.Fatal(err)
	}
	if patch.HistoryBytes != next.HistoryBytes || patch.HistoryRows != next.HistoryRows ||
		patch.HistoryEpoch != next.HistoryEpoch || patch.HistoryRevision != next.HistoryRevision {
		t.Fatalf("patch history metadata = %d/%d/%d:%d, want %d/%d/%d:%d",
			patch.HistoryRows, patch.HistoryBytes, patch.HistoryEpoch, patch.HistoryRevision,
			next.HistoryRows, next.HistoryBytes, next.HistoryEpoch, next.HistoryRevision)
	}
	got, err := ApplyPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, next) {
		t.Fatalf("patched frame differs from canonical frame\ngot:  %#v\nwant: %#v", got, next)
	}
	return patch
}

func TestPatchRejectsWrongBaseRevision(t *testing.T) {
	t.Parallel()
	base := snapshotAfterWrites(t, []byte("base"))
	patch := Patch{
		BaseRevision: base.Revision + 1,
		Revision:     base.Revision + 2,
		Columns:      base.Columns,
		Rows:         base.Rows,
	}
	if _, err := ApplyPatch(base, patch); !errors.Is(err, ErrPatchBaseMismatch) {
		t.Fatalf("ApplyPatch error = %v, want base mismatch", err)
	}
}

func TestPatchRejectsResizeAndDuplicateRows(t *testing.T) {
	t.Parallel()
	base := snapshotAfterWrites(t, []byte("base"))
	resized := cloneFrame(base)
	resized.Revision++
	resized.Columns++
	if _, err := Diff(base, resized); !errors.Is(err, ErrFullSnapshotNeeded) {
		t.Fatalf("Diff resize error = %v, want full snapshot", err)
	}

	row := cloneRow(base.Screen[0])
	patch := Patch{
		BaseRevision: base.Revision,
		Revision:     base.Revision + 1,
		Columns:      base.Columns,
		Rows:         base.Rows,
		ChangedRows: []ChangedRow{
			{Index: 0, Row: row},
			{Index: 0, Row: row},
		},
	}
	if _, err := ApplyPatch(base, patch); !errors.Is(err, ErrDuplicatePatchRow) {
		t.Fatalf("ApplyPatch duplicate error = %v, want duplicate row", err)
	}
}

func TestApplyPatchDoesNotAliasBaseOrPatch(t *testing.T) {
	t.Parallel()
	base := snapshotAfterWrites(t, []byte("base"))
	next := snapshotAfterWrites(t, []byte("next"))
	next.Revision = base.Revision + 1
	patch, err := Diff(base, next)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ApplyPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}

	patch.ChangedRows[0].Row.Cells[0].Text = "X"
	got.Screen[0].Cells[0].Text = "Y"
	if base.Screen[0].Cells[0].Text != "b" {
		t.Fatalf("base mutated through patch result: %q", base.Screen[0].Cells[0].Text)
	}
	if next.Screen[0].Cells[0].Text != "n" {
		t.Fatalf("canonical next frame mutated: %q", next.Screen[0].Cells[0].Text)
	}
}
