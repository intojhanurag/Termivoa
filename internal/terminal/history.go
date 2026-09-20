package terminal

import (
	"errors"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

const MaxHistoryPageRows = 200

var ErrInvalidHistoryRequest = errors.New("terminal history request is outside bounds")

// HistoryReadRequest identifies one page in the immutable history snapshot
// advertised by a screen frame. Offset is oldest-first.
type HistoryReadRequest struct {
	Epoch    uint64
	Revision uint64
	Offset   int
	Limit    int
}

// HistoryReadResult is copied completely inside the terminal actor. Changed
// responses carry the current token and no rows, allowing a caller to emit
// history.changed without splicing two snapshots.
type HistoryReadResult struct {
	Changed       bool
	Epoch         uint64
	Revision      uint64
	Columns       int
	Offset        int
	Rows          []Row
	NextOffset    *int
	Complete      bool
	RetainedRows  int
	RetainedBytes int
}

type historyState struct {
	epoch    uint64
	revision uint64
}

func newHistoryState() *historyState {
	return &historyState{epoch: 1}
}

func (state *historyState) changed(reset bool) {
	if reset {
		state.epoch++
		state.revision = 0
		return
	}
	state.revision++
}

func validateHistoryRead(request HistoryReadRequest) error {
	if request.Offset < 0 || request.Offset >= MaxHistoryRows ||
		request.Limit < 1 || request.Limit > MaxHistoryPageRows {
		return fmt.Errorf("%w: offset must be 0-%d and limit 1-%d",
			ErrInvalidHistoryRequest, MaxHistoryRows-1, MaxHistoryPageRows)
	}
	return nil
}

func readHistory(
	emulator *vt.Emulator,
	state *historyState,
	request HistoryReadRequest,
	maxCellBytes int,
) HistoryReadResult {
	result := HistoryReadResult{
		Changed:       request.Epoch != state.epoch || request.Revision != state.revision,
		Epoch:         state.epoch,
		Revision:      state.revision,
		Columns:       emulator.Width(),
		Offset:        request.Offset,
		RetainedRows:  emulator.ScrollbackLen(),
		RetainedBytes: emulator.ScrollbackBytes(),
	}
	if result.Changed {
		return result
	}

	start := min(request.Offset, result.RetainedRows)
	end := min(start+request.Limit, result.RetainedRows)
	result.Rows = make([]Row, 0, end-start)
	foreground := rgba(emulator.ForegroundColor())
	background := rgba(emulator.BackgroundColor())
	for y := start; y < end; y++ {
		row, _ := normalizeRow(
			emulator.Width(),
			func(x int) *uv.Cell { return emulator.ScrollbackCellAt(x, y) },
			foreground,
			background,
			maxCellBytes,
			false,
		)
		result.Rows = append(result.Rows, row)
	}
	result.Complete = end >= result.RetainedRows
	if !result.Complete {
		next := end
		result.NextOffset = &next
	}
	return result
}
