package cli

import (
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

// ValidateDesktopFiles checks interactive handles and current output dimensions
// without changing modes, reading input, or creating a managed session. Callers
// retain both files. Attachment checks again because a window can change later.
func ValidateDesktopFiles(input, output *os.File) (columns, rows int, err error) {
	if _, err := desktopFD(input); err != nil {
		return 0, 0, fmt.Errorf("requires terminal stdin: %w", err)
	}
	fd, err := desktopFD(output)
	if err != nil {
		return 0, 0, fmt.Errorf("requires terminal stdout: %w", err)
	}
	columns, rows, err = term.GetSize(fd)
	if err != nil {
		return 0, 0, fmt.Errorf("read terminal dimensions: %w", err)
	}
	return columns, rows, nil
}
