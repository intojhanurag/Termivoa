package desktop

import (
	"context"
	"errors"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// MaxPendingInputBytes bounds incomplete terminal-control input independently
// from the complete paste limit enforced by the shared input protocol.
const MaxPendingInputBytes = 64 * 1024

// BoundedInput decodes local input with limits applied inside Ultraviolet,
// before it accumulates bracketed paste or incomplete terminal sequences.
// The caller owns input and closes it after StreamEvents returns.
type BoundedInput struct{ reader *uv.TerminalReader }

// NewBoundedInput requires real context-aware input, such as the CLI's owned
// native terminal/pipe reader. An arbitrary io.Reader cannot stop an active
// OS read when an oversized paste closes the attachment.
func NewBoundedInput(input uv.ContextReader, terminalType string) (*BoundedInput, error) {
	if input == nil {
		return nil, errors.New("desktop: bounded input requires a context-aware reader")
	}
	reader := uv.NewContextTerminalReader(input, terminalType)
	reader.MaxPasteBytes = protocol.MaxPasteBytes
	reader.MaxPendingBytes = MaxPendingInputBytes
	return &BoundedInput{reader: reader}, nil
}

func (input *BoundedInput) StreamEvents(ctx context.Context, events chan<- uv.Event) error {
	if ctx == nil {
		return errors.New("desktop: input context must not be nil")
	}
	return input.reader.StreamEvents(ctx, events)
}
