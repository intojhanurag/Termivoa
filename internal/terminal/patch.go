package terminal

import (
	"errors"
	"fmt"
	"slices"
)

var (
	ErrPatchBaseMismatch  = errors.New("screen patch base revision does not match")
	ErrPatchRevision      = errors.New("screen patch revision must advance")
	ErrFullSnapshotNeeded = errors.New("screen dimensions changed; full snapshot required")
	ErrInvalidPatchRow    = errors.New("screen patch row is invalid")
	ErrDuplicatePatchRow  = errors.New("screen patch contains a duplicate row")
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
	BaseRevision   uint64
	Revision       uint64
	Columns        int
	Rows           int
	Alternate      bool
	WidthMethod    string
	Foreground     RGBA
	Background     RGBA
	CursorColor    RGBA
	Cursor         Cursor
	ChangedRows    []ChangedRow
	ContentClipped bool
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
		BaseRevision:   base.Revision,
		Revision:       next.Revision,
		Columns:        next.Columns,
		Rows:           next.Rows,
		Alternate:      next.Alternate,
		WidthMethod:    next.WidthMethod,
		Foreground:     next.Foreground,
		Background:     next.Background,
		CursorColor:    next.CursorColor,
		Cursor:         next.Cursor,
		ContentClipped: next.ContentClipped,
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

// ApplyPatch returns a new immutable frame. It never partially mutates base.
func ApplyPatch(base Frame, patch Patch) (Frame, error) {
	if patch.BaseRevision != base.Revision {
		return Frame{}, ErrPatchBaseMismatch
	}
	if patch.Revision <= patch.BaseRevision {
		return Frame{}, ErrPatchRevision
	}
	if patch.Columns != base.Columns || patch.Rows != base.Rows {
		return Frame{}, ErrFullSnapshotNeeded
	}
	if len(base.Screen) != base.Rows {
		return Frame{}, fmt.Errorf("%w: base row count does not match dimensions", ErrInvalidPatchRow)
	}

	next := cloneFrame(base)
	next.Revision = patch.Revision
	next.Alternate = patch.Alternate
	next.WidthMethod = patch.WidthMethod
	next.Foreground = patch.Foreground
	next.Background = patch.Background
	next.CursorColor = patch.CursorColor
	next.Cursor = patch.Cursor
	next.ContentClipped = patch.ContentClipped

	seen := make([]bool, patch.Rows)
	for _, changed := range patch.ChangedRows {
		if changed.Index < 0 || changed.Index >= patch.Rows || len(changed.Row.Cells) != patch.Columns {
			return Frame{}, fmt.Errorf("%w: index %d", ErrInvalidPatchRow, changed.Index)
		}
		if seen[changed.Index] {
			return Frame{}, fmt.Errorf("%w: index %d", ErrDuplicatePatchRow, changed.Index)
		}
		seen[changed.Index] = true
		next.Screen[changed.Index] = cloneRow(changed.Row)
	}
	return next, nil
}

func cloneFrame(frame Frame) Frame {
	cloned := frame
	cloned.Screen = make([]Row, len(frame.Screen))
	for index, row := range frame.Screen {
		cloned.Screen[index] = cloneRow(row)
	}
	return cloned
}

func cloneRow(row Row) Row {
	return Row{Cells: slices.Clone(row.Cells)}
}
