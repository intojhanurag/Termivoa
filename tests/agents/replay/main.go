//go:build agents

// Command replay renders a `script` typescript of `pterm attach` output with
// the project's vt emulator and prints the final screen with row numbers, so
// the desktop view can be compared with the browser driver's /screen.
//
//	go run -tags agents ./tests/agents/replay -cols 120 -rows 40 file [skipBytes]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/vt"
)

func main() {
	cols := flag.Int("cols", 120, "outer terminal columns")
	rows := flag.Int("rows", 40, "outer terminal rows")
	flag.Parse()
	data, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if skip, err := strconv.Atoi(flag.Arg(1)); err == nil && skip < len(data) {
		data = data[skip:]
	}
	emulator := vt.NewEmulator(*cols, *rows)
	go func() { _, _ = io.Copy(io.Discard, emulator) }() // drain query replies
	_, _ = emulator.Write(data)
	cursor := emulator.CursorPosition()
	fmt.Printf("bytes=%d cursor=%d,%d\n", len(data), cursor.Y, cursor.X)
	for i, line := range strings.Split(emulator.String(), "\n") {
		fmt.Printf("%02d|%s\n", i, strings.TrimRight(line, " "))
	}
}
