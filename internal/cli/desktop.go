package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/liveclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const desktopIOTimeout = 5 * time.Second

// DesktopConfig attaches to an already resolved session generation. Input and
// Output remain owned by the caller; only duplicate descriptors are closed.
type DesktopConfig struct {
	RuntimeDir, ClientBuild string
	Session                 protocol.BrokerRPCSessionRecord
	Input, Output           *os.File
	Control                 bool
}

// AttachDesktop never creates a process, retries input, or acquires control
// implicitly. A connection closes before its terminal modes are restored.
func AttachDesktop(ctx context.Context, config DesktopConfig) (returnErr error) {
	if ctx == nil {
		return errors.New("desktop attachment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	inputFD, err := desktopFD(config.Input)
	if err != nil {
		return fmt.Errorf("attach requires terminal stdin: %w", err)
	}
	outputFD, err := desktopFD(config.Output)
	if err != nil {
		return fmt.Errorf("attach requires terminal stdout: %w", err)
	}
	if !validUUID(config.Session.ID) || !validUUID(config.Session.Generation) {
		return errors.New("attach requires an exact managed session and generation")
	}
	if config.Session.State == protocol.Lost {
		return errors.New("session was lost; start a new session explicitly")
	}
	reader, err := NewFileConfirmationReader(config.Input)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, reader.Close()) }()
	writer, err := NewFileTerminalWriter(config.Output)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, writer.Close()) }()
	getSize := func() (int, int, error) { return term.GetSize(outputFD) }
	display := &desktopDisplay{writer: writer, size: getSize, controlPending: true}
	if _, _, err := getSize(); err != nil {
		return fmt.Errorf("read desktop terminal dimensions: %w", err)
	}
	instructions := "Read-only attachment: typing is ignored. Use --control to take control.\r\nCtrl+] detaches without ending the session. With control, Ctrl+V then Ctrl+] sends literal Ctrl+].\r\n"
	if config.Control {
		instructions = "Requesting control after the initial screen is applied. Input while connecting/syncing is discarded.\r\nCtrl+] detaches without ending the session. Ctrl+V then Ctrl+] sends literal Ctrl+].\r\n"
	}
	if _, err := (restorationWriter{writer}).Write([]byte(instructions)); err != nil {
		return err
	}
	tty, err := desktop.NewOuterTTY(inputFD, outputFD, restorationWriter{writer})
	if err != nil {
		return err
	}
	admission := &desktopAdmission{}
	events, err := desktop.NewAdmittedInput(reader, os.Getenv("TERM"), admission.epoch)
	if err != nil {
		return err
	}
	startupEvents, err := desktop.NewBoundedInput(reader, os.Getenv("TERM"))
	if err != nil {
		return err
	}
	attachment := desktop.Attachment{TTY: tty, Events: events, StartupEvents: startupEvents,
		BeforeInput: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Queued bytes are never sent, but the local detach chord still works.
			detach, err := drainDesktopDetach(reader.file.handle)
			if err != nil {
				return err
			}
			if err := discardDesktopInput(reader.file.handle); err != nil {
				return err
			}
			if detach {
				return desktop.ErrDetached
			}
			return nil
		},
	}
	attachment.InputRejected = display.rejectInput
	attachment.Activate = func(ctx context.Context) error {
		return display.activate(ctx, func() { admission.enabled.Store(true) })
	}
	attachment.Connect = func(connectionContext context.Context) (desktop.AttachmentTransport, error) {
		client, err := liveclient.Dial(connectionContext, liveclient.Config{
			RuntimeDir: config.RuntimeDir, ClientBuild: config.ClientBuild,
			SessionID: config.Session.ID, Generation: config.Session.Generation,
			Output: display, ValidateScreen: display.validate,
		})
		if err != nil {
			return nil, err
		}
		display.setClient(client)
		readyContext, cancelReady := context.WithTimeout(connectionContext, 15*time.Second)
		defer cancelReady()
		if err := client.WaitReady(readyContext); err != nil {
			// A rejected attach (for example the viewer limit) is also the
			// close reason; report the broker's message once.
			if closeErr := client.Close(); closeErr != err {
				err = errors.Join(err, closeErr)
			}
			return nil, err
		}
		transport := newDesktopTransport(connectionContext, client, display)
		transport.admission = admission.epoch
		if config.Control {
			if err := client.AcquireControl(readyContext); err != nil {
				return nil, errors.Join(err, transport.Close())
			}
			if err := transport.resize(readyContext); err != nil {
				return nil, errors.Join(err, transport.Close())
			}
			if err := finishDesktopStartup(readyContext, client, display); err != nil {
				return nil, errors.Join(err, transport.Close())
			}
		}
		if err := display.status(connectionContext); err != nil {
			return nil, errors.Join(err, transport.Close())
		}
		admission.client = client // Published before the live decoder starts.
		transport.start()         // Always started before exposing Close to Attachment.
		return transport, nil
	}
	returnErr = attachment.Run(ctx)
	// Run has joined both decoder and painter and restored native modes. This
	// final message is outside the alternate screen, with its own bounded I/O.
	_, messageErr := (restorationWriter{writer}).Write([]byte(display.closedMessage()))
	return errors.Join(returnErr, messageErr)
}

// closedMessage reports the session outcome the host last announced. Detach
// and connection loss leave the managed session running.
func (display *desktopDisplay) closedMessage() string {
	display.mu.Lock()
	client := display.client
	display.mu.Unlock()
	if client == nil {
		return desktopClosedMessage(liveclient.State{})
	}
	return desktopClosedMessage(client.State())
}

func desktopClosedMessage(state liveclient.State) string {
	switch state.Lifecycle {
	case protocol.Exited:
		// Go reports -1 for a process ended by a signal (pterm terminate).
		if state.ExitCode != nil && *state.ExitCode >= 0 {
			return fmt.Sprintf("Session ended (exit code %d).\r\n", *state.ExitCode)
		}
		return "Session ended.\r\n"
	case protocol.Lost:
		return "Session was lost; start a new session explicitly.\r\n"
	}
	return "Attachment closed; the managed session was not terminated.\r\n"
}

// The startup decoder is still draining/discarding input throughout this
// write. Attachment joins it and discards any remaining native input only
// after Connect returns, before starting a fresh live decoder.
func finishDesktopStartup(ctx context.Context, client interface{ WaitReady(context.Context) error }, display *desktopDisplay) error {
	if err := client.WaitReady(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return display.status(ctx)
}

type desktopAdmission struct {
	enabled atomic.Bool
	client  desktopConnection
}

func (admission *desktopAdmission) epoch() uint64 {
	if !admission.enabled.Load() {
		return 0
	}
	state := admission.client.State()
	if !state.Connected || !state.Ready || !state.Controller {
		return 0
	}
	return state.InputEpoch
}

func desktopFD(file *os.File) (uintptr, error) {
	if file == nil {
		return 0, errors.New("terminal file is missing")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return 0, err
	}
	var fd uintptr
	var terminal bool
	if err := raw.Control(func(value uintptr) { fd, terminal = value, term.IsTerminal(value) }); err != nil {
		return 0, err
	}
	if !terminal {
		return 0, errors.New("redirected files and pipes are not interactive terminals")
	}
	return fd, nil
}

// OuterTTY's synchronous interface gets a fresh bounded context per call;
// canceling the command must not disable restoration writes.
type restorationWriter struct{ writer liveclient.Output }

func (writer restorationWriter) Write(data []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), desktopIOTimeout)
	defer cancel()
	return writer.writer.WriteContext(ctx, data)
}

type desktopConnection interface {
	desktop.AttachmentTransport
	State() liveclient.State
	Changed() <-chan struct{}
	Resize(context.Context, int, int) error
	SendAdmitted(context.Context, desktop.InputAction, uint64) error
}

type desktopTransport struct {
	client    desktopConnection
	display   *desktopDisplay
	ctx       context.Context
	cancel    context.CancelFunc
	gate      chan struct{}
	sending   chan struct{}
	done      chan struct{}
	wg        sync.WaitGroup
	once      sync.Once
	mu        sync.Mutex
	err       error
	admission func() uint64
}

func newDesktopTransport(parent context.Context, client desktopConnection, display *desktopDisplay) *desktopTransport {
	ctx, cancel := context.WithCancel(parent)
	return &desktopTransport{client: client, display: display, ctx: ctx, cancel: cancel,
		gate: make(chan struct{}, 1), sending: make(chan struct{}, 1), done: make(chan struct{})}
}

func (transport *desktopTransport) start() {
	transport.wg.Add(1)
	go func() {
		defer transport.wg.Done()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-transport.ctx.Done():
				return
			case <-transport.client.Done():
				transport.finish(transport.client.Err())
				return
			case <-transport.client.Changed():
				if err := transport.display.status(transport.ctx); err != nil {
					transport.finish(err)
					return
				}
			case <-ticker.C:
				if err := transport.display.expireNotice(transport.ctx); err != nil {
					transport.finish(err)
					return
				}
				// Coalesce local resize measurements, never input. A resize waits
				// until the current input acknowledgement has completed.
				select {
				case transport.gate <- struct{}{}:
					err := transport.resize(transport.ctx)
					<-transport.gate
					if err != nil {
						transport.finish(err)
						return
					}
				default:
				}
			}
		}
	}()
}

func (transport *desktopTransport) Send(ctx context.Context, action desktop.InputAction) error {
	return transport.send(ctx, action, 0)
}

func (transport *desktopTransport) AdmissionEpoch() uint64 {
	if transport.admission != nil {
		return transport.admission()
	}
	state := transport.client.State()
	if !state.Connected || !state.Ready || !state.Controller {
		return 0
	}
	return state.InputEpoch
}

func (transport *desktopTransport) SendAdmitted(ctx context.Context, action desktop.InputAction, epoch uint64) error {
	if epoch == 0 || epoch != transport.AdmissionEpoch() {
		return nil
	}
	return transport.send(ctx, action, epoch)
}

func (transport *desktopTransport) send(ctx context.Context, action desktop.InputAction, epoch uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case transport.sending <- struct{}{}:
		defer func() { <-transport.sending }()
	default:
		return liveclient.ErrBusy
	}
	state := transport.client.State()
	if !state.Connected {
		return liveclient.ErrClosed
	}
	if !state.Ready || !state.Controller || (epoch != 0 && epoch != state.InputEpoch) {
		return nil // Observer actions are discarded, never retained or replayed.
	}
	if !transport.display.fits() {
		return transport.display.status(ctx) // No keystroke is held for the new grid.
	}
	select {
	case transport.gate <- struct{}{}:
		defer func() { <-transport.gate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-transport.ctx.Done():
		return liveclient.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if transport.ctx.Err() != nil {
		return liveclient.ErrClosed
	}
	// A single in-flight local action may wait for a resize write. Recheck
	// ownership afterwards; no key is carried across a takeover or reconnect.
	current := transport.client.State()
	if !current.Connected {
		return liveclient.ErrClosed
	}
	if !current.Ready || !current.Controller || current.LeaseEpoch != state.LeaseEpoch {
		return nil
	}
	var err error
	if epoch != 0 {
		err = transport.client.SendAdmitted(ctx, action, epoch)
	} else {
		err = transport.client.Send(ctx, action)
	}
	if inactiveDesktopControl(err) {
		return nil
	}
	return err
}

func inactiveDesktopControl(err error) bool {
	if err == liveclient.ErrNotController || err == liveclient.ErrNotReady {
		return true
	}
	remote, ok := err.(*liveclient.RemoteError)
	return ok && (remote.Code == protocol.NotController || remote.Code == protocol.StaleLease)
}

func (transport *desktopTransport) resize(ctx context.Context) error {
	state := transport.client.State()
	if !state.Connected || !state.Ready || !state.Controller {
		return nil
	}
	columns, rows, err := transport.display.size()
	if err != nil {
		return err
	}
	// A spare row shows local control state. At the protocol minimum all rows
	// remain available to the child, and instructions remain outside the TUI.
	if rows > protocol.MinRows {
		rows--
	}
	columns, rows = min(columns, protocol.MaxColumns), min(rows, protocol.MaxRows)
	if columns < protocol.MinColumns || rows < protocol.MinRows {
		return errors.New("desktop terminal is too small; enlarge it to at least 20 columns by 5 rows and re-attach")
	}
	// The paint worker may already have recovered a physical shrink before
	// this poll runs, or the outer window may already have grown back to a
	// cached old size. Only a ready matching canonical grid needs no resync.
	if transport.display.matches(columns, rows) {
		return nil
	}
	err = transport.client.Resize(ctx, columns, rows)
	if inactiveDesktopControl(err) {
		return nil
	}
	return err
}

func (transport *desktopTransport) finish(err error) {
	if err != nil && err != context.Canceled {
		transport.mu.Lock()
		transport.err = errors.Join(transport.err, err)
		transport.mu.Unlock()
	}
	transport.once.Do(func() {
		transport.cancel()
		close(transport.done)
	})
}

func (transport *desktopTransport) Done() <-chan struct{} { return transport.done }
func (transport *desktopTransport) Err() error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.err
}

func (transport *desktopTransport) Close() error {
	transport.finish(nil)
	err := transport.client.Close() // Joins the painter before TTY restoration.
	transport.wg.Wait()
	// Attachment cancels its event context before Close. Only a bare normal
	// cancellation is benign; joined native cleanup/delivery errors survive.
	if err == context.Canceled {
		err = nil
	}
	localErr := transport.Err()
	if localErr == context.Canceled {
		localErr = nil
	}
	return errors.Join(err, localErr)
}
