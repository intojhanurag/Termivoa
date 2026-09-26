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
	modes         displayModes
	modesActive   bool
}

type displayModes interface {
	Enter() error
	PrepareRestore() error
	Restore() error
}

// NewOuterTTY creates a terminal-mode guard for an attachment.
func NewOuterTTY(inputFD, outputFD uintptr, output io.Writer) (*OuterTTY, error) {
	if output == nil {
		return nil, errors.New("desktop outer terminal writer must not be nil")
	}
	return &OuterTTY{fd: inputFD, output: output, ops: systemTTYOperations, modes: newDisplayModes(outputFD)}, nil
}

// Enter switches stdin to raw mode and establishes the attachment display.
func (tty *OuterTTY) Enter() error {
	tty.mu.Lock()
	defer tty.mu.Unlock()
	if tty.rawActive || tty.displayActive || tty.modesActive {
		return errors.New("desktop outer terminal is already entered")
	}
	state, err := tty.ops.makeRaw(tty.fd)
	if err != nil {
		return fmt.Errorf("desktop: enter raw mode: %w", err)
	}
	tty.state = state
	tty.rawActive = true
	if tty.modes != nil {
		tty.modesActive = true
		if err := tty.modes.Enter(); err != nil {
			return errors.Join(fmt.Errorf("desktop: configure terminal display: %w", err), tty.restoreLocked())
		}
	}
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
	if !tty.rawActive && !tty.displayActive && !tty.modesActive {
		return nil
	}
	var sequenceErr error
	if tty.displayActive {
		if tty.modes != nil {
			// A prior failed sequence write still restores native modes. On a
			// retry re-enable drawing without replacing the original values.
			tty.modesActive = true
			sequenceErr = tty.modes.PrepareRestore()
		}
		if sequenceErr == nil {
			_, sequenceErr = io.WriteString(tty.output, restoreOuterTerminal)
		}
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
	var modesErr error
	if tty.modesActive {
		modesErr = tty.modes.Restore()
		if modesErr == nil {
			tty.modesActive = false
		} else {
			modesErr = fmt.Errorf("desktop: restore console display modes: %w", modesErr)
		}
	}
	return errors.Join(sequenceErr, restoreErr, modesErr)
}

// AttachmentTransport owns the live connection and any screen-paint workers.
// Close must cancel and join those workers before returning. Neither Close nor
// a lost connection terminates the managed child process.
type AttachmentTransport interface {
	Transport
	io.Closer
	Done() <-chan struct{}
	Err() error
}

// Attachment runs decoded outer-terminal input until cancellation, a reader
// error, connection loss, or the local detach chord. Events must honor context
// cancellation and stop all its reads before StreamEvents returns. Connect is
// called only after entering the outer terminal, so a new connection cannot
// paint before the alternate screen is active. If Connect returns an error it
// must close any connection it created.
type Attachment struct {
	TTY     *OuterTTY
	Events  uv.EventStreamer
	Connect func(context.Context) (AttachmentTransport, error)
	// StartupEvents is a separate, fresh decoder over the same owned input.
	// It drains setup input without sending it, but still handles local detach.
	// It is canceled and joined before BeforeInput and Events start. BeforeInput
	// must discard remaining native raw input at that handoff, or fail closed.
	// It returns ErrDetached if that discarded input contained Ctrl+].
	// Neither decoder may close the caller's original terminal descriptor.
	StartupEvents uv.EventStreamer
	BeforeInput   func(context.Context) error
	// Activate writes final ready status while the live decoder is already
	// draining gated input. It must honor cancellation; no post-activation
	// native flush/parser reset may discard newly advertised live input.
	Activate func(context.Context) error
}

// Run enters and always restores the outer terminal before returning.
func (attachment *Attachment) Run(ctx context.Context) (returnErr error) {
	if ctx == nil {
		return errors.New("desktop attachment context must not be nil")
	}
	if attachment.TTY == nil || attachment.Events == nil || attachment.Connect == nil {
		return errors.New("desktop attachment requires TTY, event streamer, and connector")
	}
	if (attachment.StartupEvents == nil) != (attachment.BeforeInput == nil) {
		return errors.New("desktop startup input requires both a separate decoder and handoff")
	}
	if err := attachment.TTY.Enter(); err != nil {
		return errors.Join(err, attachment.TTY.Restore())
	}
	defer func() {
		returnErr = errors.Join(returnErr, attachment.TTY.Restore())
	}()

	eventContext, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	transport, detached, err := attachment.connectDuringStartup(eventContext)
	if err != nil {
		return fmt.Errorf("desktop: connect terminal: %w", err)
	}
	if detached {
		return nil
	}
	if transport == nil {
		return errors.New("desktop connector returned no transport")
	}
	events := make(chan uv.Event)
	streamStopped := make(chan struct{})
	var streamErr error
	go func() {
		defer close(streamStopped)
		streamErr = attachment.Events.StreamEvents(eventContext, events)
	}()
	var activated chan struct{}
	var activationErr error
	if attachment.Activate != nil {
		activated = make(chan struct{})
		go func(done chan struct{}) {
			defer close(done)
			activationErr = attachment.Activate(eventContext)
		}(activated)
	}
	defer func() {
		// Stop painting and input before restoring the enclosing terminal.
		// Cancellation alone is insufficient: a final read/write may still
		// be running until both the transport and streamer have joined.
		cancelEvents()
		closeErr := transport.Close()
		// Ultraviolet flushes already decoded events even on cancellation and
		// its pinned reader uses unconditional sends. Drain and discard them
		// while joining; otherwise a trailing key after Ctrl+] can deadlock
		// shutdown. Never pass any of these events to the child.
		for streamStopped != nil || activated != nil {
			select {
			case _, ok := <-events:
				if !ok {
					events = nil
				}
			case <-streamStopped:
				streamStopped = nil
			case <-activated:
				activated = nil
			}
		}
		if activationErr != nil && !onlyStartupCancellation(activationErr) {
			returnErr = errors.Join(returnErr, fmt.Errorf("desktop: activate input: %w", activationErr))
		}
		if closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("desktop: close terminal connection: %w", closeErr))
		}
		if streamErr != nil && streamErr != context.Canceled {
			returnErr = errors.Join(returnErr, fmt.Errorf("desktop: read outer terminal: %w", streamErr))
		}
	}()

	controller := InputController{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-streamStopped:
			return nil
		case <-transport.Done():
			return transport.Err()
		case <-activated:
			activated = nil
			if activationErr != nil {
				return ctx.Err() // The deferred join reports the activation failure once.
			}
		case event, ok := <-events:
			if !ok {
				return nil
			}
			detached, err := controller.Handle(eventContext, event, transport)
			if err != nil {
				return err
			}
			if detached {
				return nil
			}
		}
	}
}
