//go:build windows

package desktop

import "golang.org/x/sys/windows"

func newDisplayModes(outputFD uintptr) displayModes {
	output := windows.Handle(outputFD)
	return &consoleModes{ops: consoleModeOps{
		inputCP: windows.GetConsoleCP, outputCP: windows.GetConsoleOutputCP,
		setInputCP: windows.SetConsoleCP, setOutputCP: windows.SetConsoleOutputCP,
		outputMode: func() (uint32, error) {
			var mode uint32
			err := windows.GetConsoleMode(output, &mode)
			return mode, err
		},
		setOutputMode: func(mode uint32) error { return windows.SetConsoleMode(output, mode) },
	}}
}
