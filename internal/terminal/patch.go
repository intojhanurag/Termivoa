package terminal

import (
	"errors"
	"fmt"
	"slices"
)

var (
	ErrPatchRevision      = errors.New("screen patch revision must advance")
	ErrFullSnapshotNeeded = errors.New("screen dimensions changed; full snapshot required")
	ErrInvalidPatchRow    = errors.New("screen patch row is invalid")
)

// ChangedRow replaces one complete screen row at Index. Whole-row replacement
// prevents wide-character tails and stale styles from surviving a patch.
type ChangedRow struct {
	Index int
	Row   Row
}

// Patch contains the changed rows and complete screen metadata required to
// advance a validated frame from BaseRevision to Revision.
type Patch struct {
	BaseRevision    uint64
	Revision        uint64
	Columns         int
	Rows            int
	Alternate       bool
	WidthMethod     string
	Foreground      RGBA
	Background      RGBA
	CursorColor     RGBA
	Cursor          Cursor
	HistoryBytes    int
	HistoryRows     int
	HistoryEpoch    uint64
	HistoryRevision uint64
	ChangedRows     []ChangedRow
	ContentClipped  bool
}

// Diff produces a patch between two immutable canonical frames. A resize must
// be sent as a full snapshot because xterm cannot safely apply it mid-patch.
func Diff(base, next Frame) (Patch, error) {
	if base.Columns != next.Columns || base.Rows != next.Rows {
		return Patch{}, ErrFullSnapshotNeeded
	}
	if next.Revision <= base.Revision {
		return Patch{}, ErrPatchRevision
	}
	if len(base.Screen) != base.Rows || len(next.Screen) != next.Rows {
		return Patch{}, fmt.Errorf("%w: frame row count does not match dimensions", ErrInvalidPatchRow)
	}

	patch := Patch{
		BaseRevision:    base.Revision,
		Revision:        next.Revision,
		Columns:         next.Columns,
		Rows:            next.Rows,
		Alternate:       next.Alternate,
		WidthMethod:     next.WidthMethod,
		Foreground:      next.Foreground,
		Background:      next.Background,
		CursorColor:     next.CursorColor,
		Cursor:          next.Cursor,
		HistoryBytes:    next.HistoryBytes,
		HistoryRows:     next.HistoryRows,
		HistoryEpoch:    next.HistoryEpoch,
		HistoryRevision: next.HistoryRevision,
		ContentClipped:  next.ContentClipped,
	}
	for index := range next.Screen {
		if !slices.Equal(base.Screen[index].Cells, next.Screen[index].Cells) {
			patch.ChangedRows = append(patch.ChangedRows, ChangedRow{
				Index: index,
				Row:   cloneRow(next.Screen[index]),
			})
		}
	}
	return patch, nil
}

func cloneRow(row Row) Row {
	return Row{Cells: slices.Clone(row.Cells)}
}
