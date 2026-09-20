package desktop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

const (
	enterOuterTerminal   = "\x1b[?1049h\x1b[?2004h\x1b[?7l\x1b[2J\x1b[H"
	restoreOuterTerminal = "\x1b[0m\x1b[?25h\x1b[?7h\x1b[?2004l\x1b[?1049l"
)

type ttyOperations struct {
	makeRaw func(uintptr) (*term.State, error)
	restore func(uintptr, *term.State) error
}

var systemTTYOperations = ttyOperations{
	makeRaw: term.MakeRaw,
	restore: term.Restore,
}

// OuterTTY owns reversible changes made to the user's desktop terminal.
type OuterTTY struct {
	fd            uintptr
	output        io.Writer
	ops           ttyOperations
	state         *term.State
	mu            sync.Mutex
	rawActive     bool
	displayActive bool
}

// NewOuterTTY creates a terminal-mode guard for an attachment.
func NewOuterTTY(fd uintptr, output io.Writer) (*OuterTTY, error) {
	if output == nil {
		return nil, errors.New("desktop outer terminal writer must not be nil")
	}
	return &OuterTTY{fd: fd, output: output, ops: systemTTYOperations}, nil
}

// Enter switches stdin to raw mode and establishes the attachment display.
func (tty *OuterTTY) Enter() error {
	tty.mu.Lock()
	defer tty.mu.Unlock()
	if tty.rawActive || tty.displayActive {
		return errors.New("desktop outer terminal is already entered")
	}
	state, err := tty.ops.makeRaw(tty.fd)
	if err != nil {
		return fmt.Errorf("desktop: enter raw mode: %w", err)
	}
	tty.state = state
	tty.rawActive = true
	tty.displayActive = true
	if _, err := io.WriteString(tty.output, enterOuterTerminal); err != nil {
		restoreErr := tty.restoreLocked()
		return errors.Join(fmt.Errorf("desktop: initialize outer terminal: %w", err), restoreErr)
	}
	return nil
}

// Restore returns the outer terminal to its previous modes. It is idempotent.
func (tty *OuterTTY) Restore() error {
	tty.mu.Lock()
	defer tty.mu.Unlock()
	return tty.restoreLocked()
}

func (tty *OuterTTY) restoreLocked() error {
	if !tty.rawActive && !tty.displayActive {
		return nil
	}
	var sequenceErr error
	if tty.displayActive {
		_, sequenceErr = io.WriteString(tty.output, restoreOuterTerminal)
		if sequenceErr == nil {
			tty.displayActive = false
		} else {
			sequenceErr = fmt.Errorf("desktop: restore outer terminal sequences: %w", sequenceErr)
		}
	}
	var restoreErr error
	if tty.rawActive {
		restoreErr = tty.ops.restore(tty.fd, tty.state)
		if restoreErr == nil {
			tty.rawActive = false
			tty.state = nil
		} else {
			restoreErr = fmt.Errorf("desktop: restore raw mode: %w", restoreErr)
		}
	}
	return errors.Join(sequenceErr, restoreErr)
}

// Attachment runs decoded outer-terminal input until cancellation, a reader
// error, or the local detach chord. The caller supplies an independently
// cancellable Ultraviolet streamer, normally backed by a cancel reader.
type Attachment struct {
	TTY       *OuterTTY
	Events    uv.EventStreamer
	Transport Transport
}

// Run enters and always restores the outer terminal before returning.
func (attachment *Attachment) Run(ctx context.Context) (returnErr error) {
	if ctx == nil {
		return errors.New("desktop attachment context must not be nil")
	}
	if attachment.TTY == nil || attachment.Events == nil || attachment.Transport == nil {
		return errors.New("desktop attachment requires TTY, event streamer, and transport")
	}
	if err := attachment.TTY.Enter(); err != nil {
		return errors.Join(err, attachment.TTY.Restore())
	}
	defer func() {
		returnErr = errors.Join(returnErr, attachment.TTY.Restore())
	}()

	eventContext, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	events := make(chan uv.Event)
	streamResult := make(chan error, 1)
	go func() {
		streamResult <- attachment.Events.StreamEvents(eventContext, events)
	}()

	controller := InputController{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-streamResult:
			if err != nil {
				return fmt.Errorf("desktop: read outer terminal: %w", err)
			}
			return nil
		case event := <-events:
			detached, err := controller.Handle(ctx, event, attachment.Transport)
			if err != nil {
				return err
			}
			if detached {
				return nil
			}
		}
	}
}
