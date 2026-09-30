package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/liveclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type desktopDisplay struct {
	writer         liveclient.Output
	size           func() (int, int, error)
	mu             sync.Mutex
	screen         *protocol.ScreenMetadata
	client         interface{ State() liveclient.State }
	controlPending bool
	notice         string // Static ASCII, shown in place of the control label.
	noticeUntil    time.Time
}

const desktopNoticeDuration = 3 * time.Second

// rejectInput briefly reports a dropped local action. The attachment continues.
func (display *desktopDisplay) rejectInput(ctx context.Context, err *desktop.InvalidInputError) error {
	display.mu.Lock()
	defer display.mu.Unlock()
	display.notice, display.noticeUntil = "Input not sent: invalid "+err.Kind+" | Ctrl+] detach", time.Now().Add(desktopNoticeDuration)
	return display.statusLocked(ctx)
}

// expireNotice repaints the footer once a rejection notice has lapsed.
func (display *desktopDisplay) expireNotice(ctx context.Context) error {
	display.mu.Lock()
	defer display.mu.Unlock()
	if display.notice == "" || time.Now().Before(display.noticeUntil) {
		return nil
	}
	display.notice = ""
	return display.statusLocked(ctx)
}

func (display *desktopDisplay) setClient(client interface{ State() liveclient.State }) {
	display.mu.Lock()
	display.client = client
	display.mu.Unlock()
}

func (display *desktopDisplay) validate(screen protocol.ScreenMetadata) error {
	columns, rows, err := display.size()
	if err != nil {
		return err
	}
	if err := desktopViewport(screen, columns, rows); err != nil {
		return err
	}
	display.mu.Lock()
	display.screen = &screen
	display.mu.Unlock()
	return nil
}

func desktopViewport(screen protocol.ScreenMetadata, columns, rows int) error {
	if screen.Columns > int64(columns) || screen.Rows > int64(rows) {
		cause := fmt.Errorf("session grid is %dx%d but desktop terminal is %dx%d; enlarge the window to fit and re-attach (the session is still running)", screen.Columns, screen.Rows, columns, rows)
		if columns < protocol.MinColumns || rows < protocol.MinRows {
			return cause
		}
		if rows > protocol.MinRows {
			rows--
		}
		return &liveclient.ViewportResizeRequired{Columns: min(columns, protocol.MaxColumns), Rows: min(rows, protocol.MaxRows), Cause: cause}
	}
	return nil
}

func (display *desktopDisplay) WriteContext(ctx context.Context, data []byte) (int, error) {
	display.mu.Lock()
	defer display.mu.Unlock()
	footer, err := display.footerLocked()
	if err != nil {
		return 0, err
	}
	drawing := append(append([]byte(nil), data...), footer...)
	n, err := display.writer.WriteContext(ctx, drawing)
	if err == nil && n != len(drawing) {
		err = io.ErrShortWrite
	}
	return min(n, len(data)), err
}

func (display *desktopDisplay) status(ctx context.Context) error {
	display.mu.Lock()
	defer display.mu.Unlock()
	return display.statusLocked(ctx)
}

// The live decoder already drains gated input. Nothing is flushed or reset
// after the final status is written; admission begins before releasing this
// display lock so another paint cannot advertise a premature ready footer.
// An owned terminal writer admits at the footer's last byte, not after the
// write returns: a key read in between would otherwise be dropped (#104).
// A key read while the footer write is still blocked stays dropped. A write
// that fails after its last byte (a cancellation) still ends the attachment.
func (display *desktopDisplay) activate(ctx context.Context, admit func()) error {
	display.mu.Lock()
	defer display.mu.Unlock()
	display.controlPending = false
	admit = sync.OnceFunc(admit)
	if err := display.statusLocked(withTerminalIOHook(ctx, admit)); err != nil {
		display.controlPending = true
		return err
	}
	if desktopReadyFooterWritten != nil {
		desktopReadyFooterWritten()
	}
	admit() // No footer was needed, or the writer has no I/O hook.
	return nil
}

// desktopReadyFooterWritten, when set by a test, runs after the ready footer
// write returns and before activate returns.
var desktopReadyFooterWritten func()

func (display *desktopDisplay) statusLocked(ctx context.Context) error {
	footer, err := display.footerLocked()
	state := liveclient.State{}
	if display.client != nil {
		state = display.client.State()
	}
	if _, ok := err.(*liveclient.ViewportResizeRequired); ok && (state.Controller || state.Resizing) {
		columns, _, sizeErr := display.size()
		if sizeErr != nil {
			return sizeErr
		}
		label := "Syncing | Input ignored | Ctrl+] detach"
		label = label[:min(len(label), max(0, columns))]
		// This is a local placeholder, never a canonical frame or an ack. The
		// next complete fitting snapshot replaces it and restores the cursor.
		footer, err = []byte("\x1b[?7l\x1b[0m\x1b[2J\x1b[H\x1b[?25l"+label), nil
	}
	if err != nil || len(footer) == 0 {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, desktopIOTimeout)
	defer cancel()
	_, err = display.writer.WriteContext(ctx, footer)
	return err
}

func (display *desktopDisplay) fits() bool {
	display.mu.Lock()
	defer display.mu.Unlock()
	if display.screen == nil {
		return false
	}
	columns, rows, err := display.size()
	return err == nil && desktopViewport(*display.screen, columns, rows) == nil
}

func (display *desktopDisplay) matches(columns, rows int) bool {
	display.mu.Lock()
	defer display.mu.Unlock()
	return display.screen != nil && display.screen.Columns == int64(columns) && display.screen.Rows == int64(rows)
}

func (display *desktopDisplay) footerLocked() ([]byte, error) {
	if display.screen == nil {
		return nil, nil
	}
	columns, rows, err := display.size()
	if err != nil {
		return nil, err
	}
	if err := desktopViewport(*display.screen, columns, rows); err != nil {
		return nil, err
	}
	if int64(rows) == display.screen.Rows {
		return nil, nil // Exact-fit observers keep the complete canonical grid.
	}
	label := "Read-only | Ctrl+] detach | --control to type"
	if display.client != nil {
		state := display.client.State()
		if state.Controller {
			label = "Control | Ctrl+] detach | Ctrl+V Ctrl+] sends literal"
			if display.notice != "" {
				label = display.notice
			}
		}
		if !state.Ready || display.controlPending {
			label = "Syncing | Input ignored | Ctrl+] detach"
		}
		if !state.Connected {
			label = "Disconnected | No input sent | Ctrl+] detach"
		}
	}
	label = label[:min(len(label), columns)] // Static ASCII only, never child text.
	return fmt.Appendf(nil, "\x1b[%d;1H\x1b[0m\x1b[2K%s\x1b[%d;%dH", display.screen.Rows+1, label, display.screen.Cursor.Row+1, display.screen.Cursor.Column+1), nil
}
