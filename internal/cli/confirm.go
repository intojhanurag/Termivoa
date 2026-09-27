package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const confirmationBufferBytes = 64

// ConfirmationReader must return promptly when ctx is canceled, including
// while waiting for input. Implementations own their cancellation mechanism;
// an arbitrary io.Reader, including os.Stdin, is not sufficient. The command
// that owns terminal input supplies this implementation and its cleanup.
type ConfirmationReader interface {
	ReadContext(context.Context, []byte) (int, error)
}

// ConfirmationReadFunc adapts a context-aware input function. The function
// must honor the cancellation contract of ConfirmationReader itself.
type ConfirmationReadFunc func(context.Context, []byte) (int, error)

func (read ConfirmationReadFunc) ReadContext(ctx context.Context, data []byte) (int, error) {
	return read(ctx, data)
}

// LineConfirmer accepts only y/yes (case-insensitive). Empty input, n/no, and
// EOF without a response decline. Oversized input is rejected without an
// unbounded allocation. It does not spawn a reader goroutine or close input.
type LineConfirmer struct {
	input  ConfirmationReader
	output io.Writer
}

func NewLineConfirmer(input ConfirmationReader, output io.Writer) (*LineConfirmer, error) {
	if input == nil || output == nil {
		return nil, errors.New("CLI confirmation input and output must not be nil")
	}
	return &LineConfirmer{
		input:  input,
		output: output,
	}, nil
}

func (confirmer *LineConfirmer) Confirm(ctx context.Context, prompt string) (bool, error) {
	if ctx == nil {
		return false, errors.New("CLI confirmation context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := writeString(confirmer.output, prompt); err != nil {
		return false, err
	}
	line, err := readConfirmationLine(ctx, confirmer.input)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return false, contextErr
	}
	response := strings.ToLower(strings.TrimSpace(line))
	switch response {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, errors.New("confirmation must be yes or no")
	}
}

func readConfirmationLine(ctx context.Context, input ConfirmationReader) (string, error) {
	var line [confirmationBufferBytes]byte
	length, emptyReads := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// Read no farther than the newline. Confirmation is at most 64 bytes,
		// and a later command must not lose input to speculative read-ahead.
		var next [1]byte
		n, err := input.ReadContext(ctx, next[:])
		if contextErr := ctx.Err(); contextErr != nil {
			return "", contextErr
		}
		if n < 0 || n > len(next) {
			return "", errors.New("confirmation reader returned an invalid byte count")
		}
		if n == 1 {
			emptyReads = 0
			if next[0] == '\n' {
				return string(line[:length]), nil
			}
			if length == len(line) {
				return "", errors.New("confirmation response is too long")
			}
			line[length] = next[0]
			length++
		} else {
			emptyReads++
		}
		if err != nil {
			return string(line[:length]), err
		}
		if emptyReads >= 100 {
			return "", io.ErrNoProgress
		}
	}
}
