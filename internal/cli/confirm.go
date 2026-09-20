package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

const confirmationBufferBytes = 64

// LineConfirmer accepts only y/yes (case-insensitive). Empty input, n/no, and
// EOF without a response decline. Oversized input is rejected without an
// unbounded allocation.
type LineConfirmer struct {
	input  *bufio.Reader
	output io.Writer
}

func NewLineConfirmer(input io.Reader, output io.Writer) (*LineConfirmer, error) {
	if input == nil || output == nil {
		return nil, errors.New("CLI confirmation input and output must not be nil")
	}
	return &LineConfirmer{
		input:  bufio.NewReaderSize(input, confirmationBufferBytes),
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
	line, prefix, err := confirmer.input.ReadLine()
	if prefix {
		return false, errors.New("confirmation response is too long")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return false, contextErr
	}
	response := strings.ToLower(strings.TrimSpace(string(line)))
	switch response {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, errors.New("confirmation must be yes or no")
	}
}
