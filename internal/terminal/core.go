package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

const (
	MinColumns = 20
	MaxColumns = 400
	MinRows    = 5
	MaxRows    = 200

	MaxPasteBytes       = 64 * 1024
	MaxTextBytes        = 64 * 1024
	MaxOutputChunkBytes = 256 * 1024
	MaxCellContentBytes = 1024
	MaxLinkBytes        = 2048
	MaxSequenceBytes    = 64 * 1024
	MaxHistoryRows      = 5000
	MaxHistoryBytes     = 32 * 1024 * 1024
	MaxCommandQueue     = 1024
	MaxOutputQueue      = 1024

	defaultCommandQueue = 128
	defaultOutputQueue  = 128
)

var (
	ErrClosed          = errors.New("terminal core is closed")
	ErrInvalidSize     = errors.New("terminal dimensions are out of bounds")
	ErrInvalidText     = errors.New("terminal text contains invalid UTF-8 or control characters")
	ErrInvalidPaste    = errors.New("terminal paste contains invalid UTF-8 or disallowed control characters")
	ErrPayloadTooLarge = errors.New("terminal input payload is too large")
)

// Config controls bounded resources owned by a Core.
type Config struct {
	Columns        int
	Rows           int
	CommandQueue   int
	OutputQueue    int
	ScrollbackRows int
	HistoryBytes   int
	MaxCellBytes   int
}

// Core serializes all emulator state changes through a single actor.
// Output must have exactly one consumer, normally the broker's PTY writer.
type Core struct {
	commands  chan command
	output    chan []byte
	done      chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once
}

type commandKind uint8

const (
	commandOutput commandKind = iota
	commandResize
	commandSnapshot
	commandScreenSnapshot
	commandText
	commandPaste
	commandKey
	commandMouse
)

type command struct {
	kind    commandKind
	data    []byte
	text    string
	columns int
	rows    int
	key     uv.KeyEvent
	mouse   uv.MouseEvent
	reply   chan result
}

type result struct {
	frame Frame
	err   error
}

// New creates and starts an authoritative emulator. The supplied context owns
// its lifetime. Callers must drain Output continuously, even with no viewers.
func New(ctx context.Context, config Config) (*Core, error) {
	if ctx == nil {
		return nil, errors.New("terminal context must not be nil")
	}
	if err := validateSize(config.Columns, config.Rows); err != nil {
		return nil, err
	}
	if config.CommandQueue <= 0 {
		config.CommandQueue = defaultCommandQueue
	} else if config.CommandQueue > MaxCommandQueue {
		return nil, fmt.Errorf("command queue exceeds maximum %d", MaxCommandQueue)
	}
	if config.OutputQueue <= 0 {
		config.OutputQueue = defaultOutputQueue
	} else if config.OutputQueue > MaxOutputQueue {
		return nil, fmt.Errorf("output queue exceeds maximum %d", MaxOutputQueue)
	}
	if config.ScrollbackRows <= 0 || config.ScrollbackRows > MaxHistoryRows {
		config.ScrollbackRows = MaxHistoryRows
	}
	if config.MaxCellBytes <= 0 || config.MaxCellBytes > MaxCellContentBytes {
		config.MaxCellBytes = MaxCellContentBytes
	}
	if config.HistoryBytes <= 0 || config.HistoryBytes > MaxHistoryBytes {
		config.HistoryBytes = MaxHistoryBytes
	}

	actorContext, cancelActor := context.WithCancel(ctx)
	core := &Core{
		commands: make(chan command, config.CommandQueue),
		output:   make(chan []byte, config.OutputQueue),
		done:     make(chan struct{}),
		cancel:   cancelActor,
	}
	emulator := vt.NewEmulator(config.Columns, config.Rows)
	emulator.SetScrollbackSize(config.ScrollbackRows)
	emulator.SetScrollbackByteLimit(config.HistoryBytes)
	emulator.SetMaxGraphemeBytes(config.MaxCellBytes)
	emulator.SetMaxLinkBytes(MaxLinkBytes)
	emulator.SetMaxSequenceBytes(MaxSequenceBytes)

	go func() {
		<-actorContext.Done()
		closeEmulatorInput(emulator)
	}()
	go core.pumpOutput(emulator)
	go core.run(actorContext, emulator, config.MaxCellBytes, config.HistoryBytes)
	return core, nil
}

// Output returns the single ordered stream containing both encoded user input
// and emulator-generated replies. Receiving values transfers ownership of the
// byte slice to the receiver. The stream closes after Core shuts down.
func (core *Core) Output() <-chan []byte {
	return core.output
}

// WriteOutput interprets bytes read from the child PTY.
func (core *Core) WriteOutput(ctx context.Context, data []byte) error {
	for len(data) > 0 {
		chunkSize := min(len(data), MaxOutputChunkBytes)
		copyOfData := append([]byte(nil), data[:chunkSize]...)
		if err := core.execute(ctx, command{kind: commandOutput, data: copyOfData}); err != nil {
			return err
		}
		data = data[chunkSize:]
	}
	return nil
}

// Resize changes the canonical terminal dimensions after validating bounds.
func (core *Core) Resize(ctx context.Context, columns, rows int) error {
	if err := validateSize(columns, rows); err != nil {
		return err
	}
	return core.execute(ctx, command{kind: commandResize, columns: columns, rows: rows})
}

// Snapshot captures an immutable copy of canonical screen and history state.
func (core *Core) Snapshot(ctx context.Context) (Frame, error) {
	result, err := core.request(ctx, command{kind: commandSnapshot})
	return result.frame, err
}

// ScreenSnapshot captures only the live canonical grid. It is intended for
// frequent viewer publication and deliberately avoids copying retained
// history, which is paged independently by the broker.
func (core *Core) ScreenSnapshot(ctx context.Context) (Frame, error) {
	result, err := core.request(ctx, command{kind: commandScreenSnapshot})
	return result.frame, err
}

// SendText encodes committed printable UTF-8 through the authoritative
// emulator. Control actions must use SendKey.
func (core *Core) SendText(ctx context.Context, text string) error {
	if len(text) > MaxTextBytes {
		return ErrPayloadTooLarge
	}
	if !validText(text) {
		return ErrInvalidText
	}
	return core.execute(ctx, command{kind: commandText, text: text})
}

// Paste encodes a bounded paste through the authoritative emulator, including
// bracketed-paste mode when the child enabled it.
func (core *Core) Paste(ctx context.Context, text string) error {
	if len(text) > MaxPasteBytes {
		return ErrPayloadTooLarge
	}
	if !validPaste(text) {
		return ErrInvalidPaste
	}
	return core.execute(ctx, command{kind: commandPaste, text: text})
}

// SendKey encodes a key event according to current terminal modes.
func (core *Core) SendKey(ctx context.Context, key uv.KeyEvent) error {
	if key == nil {
		return errors.New("terminal key must not be nil")
	}
	return core.execute(ctx, command{kind: commandKey, key: key})
}

// SendMouse encodes a structured mouse event according to the terminal modes
// enabled by the child. Coordinates are zero-based terminal cells.
func (core *Core) SendMouse(ctx context.Context, mouse uv.MouseEvent) error {
	if mouse == nil {
		return errors.New("terminal mouse event must not be nil")
	}
	return core.execute(ctx, command{kind: commandMouse, mouse: mouse})
}

// Close stops the actor. It is safe to call more than once.
func (core *Core) Close() error {
	core.closeOnce.Do(func() {
		core.cancel()
	})
	<-core.done
	return nil
}

func (core *Core) execute(ctx context.Context, cmd command) error {
	_, err := core.request(ctx, cmd)
	return err
}

func (core *Core) request(ctx context.Context, cmd command) (result, error) {
	if ctx == nil {
		return result{}, errors.New("terminal operation context must not be nil")
	}
	cmd.reply = make(chan result, 1)
	select {
	case core.commands <- cmd:
	case <-core.done:
		return result{}, ErrClosed
	case <-ctx.Done():
		return result{}, ctx.Err()
	}

	// Enqueue is the acceptance boundary. After it succeeds, return only the
	// actor's authoritative result or Core shutdown. Reporting ctx cancellation
	// here could make a caller retry input that the actor will still execute.
	select {
	case response := <-cmd.reply:
		return response, response.err
	case <-core.done:
		return result{}, ErrClosed
	}
}

func (core *Core) run(ctx context.Context, emulator *vt.Emulator, maxCellBytes, maxHistoryBytes int) {
	revision := uint64(0)
	defer close(core.done)
	defer closeEmulatorInput(emulator)

	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-core.commands:
			if err := ctx.Err(); err != nil {
				cmd.reply <- result{err: err}
				return
			}
			var response result
			switch cmd.kind {
			case commandOutput:
				_, response.err = emulator.WriteStream(cmd.data)
				if response.err == nil {
					revision++
				}
			case commandResize:
				emulator.Resize(cmd.columns, cmd.rows)
				revision++
			case commandSnapshot:
				emulator.Flush()
				response.frame = captureFrame(emulator, revision, maxCellBytes, maxHistoryBytes, true)
			case commandScreenSnapshot:
				emulator.Flush()
				response.frame = captureFrame(emulator, revision, maxCellBytes, maxHistoryBytes, false)
			case commandText:
				emulator.SendText(cmd.text)
			case commandPaste:
				emulator.Paste(cmd.text)
			case commandKey:
				emulator.SendKey(cmd.key)
			case commandMouse:
				emulator.SendMouse(cmd.mouse)
			default:
				response.err = fmt.Errorf("unknown terminal command %d", cmd.kind)
			}
			cmd.reply <- response
		}
	}
}

func (core *Core) pumpOutput(emulator *vt.Emulator) {
	defer close(core.output)
	buffer := make([]byte, 4096)
	for {
		count, err := emulator.Read(buffer)
		if count > 0 {
			chunk := append([]byte(nil), buffer[:count]...)
			select {
			case core.output <- chunk:
			case <-core.done:
				return
			}
		}
		if err != nil {
			break
		}
	}
	// The actor owns every emulator mutation. Waiting for it to stop before
	// Close avoids racing the dependency's unsynchronized closed flag.
	<-core.done
	_ = emulator.Close()
}

func closeEmulatorInput(emulator *vt.Emulator) {
	// Closing the public input-pipe writer wakes the response pump without
	// racing Emulator.Close against Emulator.Read's internal closed flag.
	if closer, ok := emulator.InputPipe().(io.Closer); ok {
		_ = closer.Close()
	}
}

func validateSize(columns, rows int) error {
	if columns < MinColumns || columns > MaxColumns || rows < MinRows || rows > MaxRows {
		return fmt.Errorf("%w: got %dx%d, want columns %d-%d and rows %d-%d",
			ErrInvalidSize, columns, rows, MinColumns, MaxColumns, MinRows, MaxRows)
	}
	return nil
}

func validText(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, value := range text {
		if value == 0x7f || value < 0x20 || value >= 0x80 && value <= 0x9f {
			return false
		}
	}
	return true
}

func validPaste(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	for _, value := range text {
		if value == 0 || value == 0x1b || value == 0x7f ||
			(value < 0x20 && value != '\n' && value != '\r' && value != '\t') ||
			(value >= 0x80 && value <= 0x9f) {
			return false
		}
	}
	return true
}

func cursorStyle(style vt.CursorStyle) CursorStyle {
	switch style {
	case vt.CursorUnderline:
		return CursorUnderline
	case vt.CursorBar:
		return CursorBar
	default:
		return CursorBlock
	}
}
