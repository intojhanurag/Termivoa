package desktop

import (
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// ErrResyncRequired means a desktop client must abandon its current frame and
// request a new complete snapshot.
var ErrResyncRequired = errors.New("desktop frame resynchronization required")

type snapshotAssembly struct {
	generation string
	payload    protocol.SnapshotBeginPayload
	rows       []protocol.TerminalRow
	received   []bool
}

// FrameClient owns desktop snapshot assembly and revision state. No partial
// snapshot is painted, and patches are accepted only for the exact displayed
// generation and base revision.
type FrameClient struct {
	renderer *Renderer

	mu         sync.Mutex
	assembly   *snapshotAssembly
	generation string
	revision   string
	columns    int64
	rows       int64
}

func NewFrameClient(renderer *Renderer) (*FrameClient, error) {
	if renderer == nil {
		return nil, errors.New("desktop frame client requires a renderer")
	}
	return &FrameClient{renderer: renderer}, nil
}

func (client *FrameClient) BeginSnapshot(generation string, payload protocol.SnapshotBeginPayload) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if _, err := decimalCounter(payload.Revision); err != nil {
		return resync(err)
	}
	if payload.SnapshotID == "" || generation == "" {
		return resync(errors.New("snapshot identity is empty"))
	}
	if err := validateScreen(payload.Screen); err != nil {
		return resync(err)
	}
	client.assembly = &snapshotAssembly{
		generation: generation,
		payload:    payload,
		rows:       make([]protocol.TerminalRow, payload.Screen.Rows),
		received:   make([]bool, payload.Screen.Rows),
	}
	return nil
}

func (client *FrameClient) AddSnapshotRows(generation string, payload protocol.SnapshotRowsPayload) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	assembly, err := client.matchSnapshot(generation, payload.SnapshotID, payload.Revision)
	if err != nil {
		client.assembly = nil
		return err
	}
	if len(payload.Rows) == 0 || payload.StartRow < 0 || payload.StartRow+int64(len(payload.Rows)) > int64(len(assembly.rows)) {
		client.assembly = nil
		return resync(errors.New("snapshot row chunk is outside the declared screen"))
	}
	for offset, row := range payload.Rows {
		index := int(payload.StartRow) + offset
		if assembly.received[index] {
			client.assembly = nil
			return resync(fmt.Errorf("snapshot row %d was received twice", index))
		}
		if err := validateRow(assembly.payload.Screen.Columns, row); err != nil {
			client.assembly = nil
			return resync(fmt.Errorf("snapshot row %d: %w", index, err))
		}
		assembly.rows[index] = cloneProtocolRow(row)
		assembly.received[index] = true
	}
	return nil
}

func (client *FrameClient) EndSnapshot(generation string, payload protocol.SnapshotEndPayload) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	assembly, err := client.matchSnapshot(generation, payload.SnapshotID, payload.Revision)
	if err != nil {
		client.assembly = nil
		return err
	}
	if payload.RowCount != int64(len(assembly.rows)) {
		client.assembly = nil
		return resync(errors.New("snapshot row count does not match the declared screen"))
	}
	for index, received := range assembly.received {
		if !received {
			client.assembly = nil
			return resync(fmt.Errorf("snapshot row %d is missing", index))
		}
	}
	if err := client.renderer.RenderSnapshot(assembly.payload.Screen, assembly.rows); err != nil {
		client.assembly = nil
		return err
	}
	client.generation = assembly.generation
	client.revision = assembly.payload.Revision
	client.columns = assembly.payload.Screen.Columns
	client.rows = assembly.payload.Screen.Rows
	client.assembly = nil
	return nil
}

func (client *FrameClient) ApplyPatch(generation string, payload protocol.ScreenPatchPayload) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	base, err := decimalCounter(payload.BaseRevision)
	if err != nil {
		return resync(err)
	}
	revision, err := decimalCounter(payload.Revision)
	if err != nil || revision <= base {
		return resync(errors.New("patch revision does not advance"))
	}
	if generation != client.generation || payload.BaseRevision != client.revision {
		return resync(errors.New("patch generation or base revision does not match"))
	}
	if payload.Screen.Columns != client.columns || payload.Screen.Rows != client.rows {
		return resync(errors.New("screen resize requires a new snapshot"))
	}
	if err := client.renderer.RenderPatch(payload.Screen, payload.Rows); err != nil {
		return fmt.Errorf("%w: %w", ErrResyncRequired, err)
	}
	client.revision = payload.Revision
	return nil
}

func (client *FrameClient) DisplayedRevision() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.revision
}

func (client *FrameClient) matchSnapshot(generation, snapshotID, revision string) (*snapshotAssembly, error) {
	assembly := client.assembly
	if assembly == nil || generation != assembly.generation || snapshotID != assembly.payload.SnapshotID || revision != assembly.payload.Revision {
		return nil, resync(errors.New("snapshot chunk does not match the active transaction"))
	}
	return assembly, nil
}

func decimalCounter(value string) (uint64, error) {
	if value == "" || len(value) > 20 || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("counter is not canonical decimal text")
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errors.New("counter does not fit uint64")
	}
	return parsed, nil
}

func resync(err error) error {
	return fmt.Errorf("%w: %v", ErrResyncRequired, err)
}

func cloneProtocolRow(row protocol.TerminalRow) protocol.TerminalRow {
	cloned := protocol.TerminalRow{Runs: make([]protocol.CellRun, len(row.Runs))}
	for index, run := range row.Runs {
		cloned.Runs[index] = run
		cloned.Runs[index].Cells = append([]protocol.Cell(nil), run.Cells...)
		if run.Style.UnderlineColor != nil {
			color := *run.Style.UnderlineColor
			cloned.Runs[index].Style.UnderlineColor = &color
		}
		for cellIndex, cell := range cloned.Runs[index].Cells {
			if cell.Link != nil {
				link := *cell.Link
				cloned.Runs[index].Cells[cellIndex].Link = &link
			}
		}
	}
	return cloned
}
