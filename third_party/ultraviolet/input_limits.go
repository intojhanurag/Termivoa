package uv

import (
	"context"
	"errors"
)

// ContextReader must return when ctx is canceled and must finish all reads of
// p before returning. A goroutine around an arbitrary blocking Read does not
// satisfy this contract.
type ContextReader interface {
	ReadContext(context.Context, []byte) (int, error)
}

type contextInput struct{ ContextReader }

func (input contextInput) Read(p []byte) (int, error) {
	return input.ReadContext(context.Background(), p)
}

// NewContextTerminalReader permits cancellation of the read pump on decoder
// errors. Set MaxPasteBytes and MaxPendingBytes before calling StreamEvents.
func NewContextTerminalReader(input ContextReader, termType string) *TerminalReader {
	return NewTerminalReader(contextInput{input}, termType)
}

var (
	ErrPasteLimit        = errors.New("terminal paste exceeds configured byte limit")
	ErrPendingInputLimit = errors.New("terminal input sequence exceeds configured byte limit")
)

func (d *eventScanner) appendPaste(data []byte) bool {
	if d.maxPasteBytes > 0 {
		if len(data) > d.maxPasteBytes-len(d.paste) {
			d.inputErr = ErrPasteLimit
			d.paste = nil
			return false
		}
		// Go's ordinary append growth can reserve more than the configured
		// limit. Cap allocation as well as retained content in bounded mode.
		needed := len(d.paste) + len(data)
		if needed > cap(d.paste) {
			capacity := min(d.maxPasteBytes, max(needed, max(64, cap(d.paste)*2)))
			grown := make([]byte, len(d.paste), capacity)
			copy(grown, d.paste)
			d.paste = grown
		}
	}
	d.paste = append(d.paste, data...)
	return true
}
