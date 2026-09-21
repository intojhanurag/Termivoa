package desktop

import (
	"errors"
	"fmt"
)

// Windows byte-oriented console I/O uses the current code pages. Raw input
// alone neither selects UTF-8 nor enables output escape processing. Keep the
// reversible policy testable independently from the native Windows syscalls.
const (
	consoleUTF8            = uint32(65001)
	consoleProcessedOutput = uint32(0x0001)
	consoleVTOutput        = uint32(0x0004)
)

type consoleModeOps struct {
	inputCP, outputCP, outputMode          func() (uint32, error)
	setInputCP, setOutputCP, setOutputMode func(uint32) error
}

type consoleModes struct {
	ops                                      consoleModeOps
	inputCP, outputCP, outputMode            uint32
	inputChanged, outputChanged, modeChanged bool
}

func (modes *consoleModes) Enter() error {
	if modes.inputChanged || modes.outputChanged || modes.modeChanged {
		return errors.New("console modes still require restoration")
	}
	// Capture every original value before making any change.
	var err error
	if modes.inputCP, err = modes.ops.inputCP(); err != nil {
		return fmt.Errorf("read console input code page: %w", err)
	}
	if modes.outputCP, err = modes.ops.outputCP(); err != nil {
		return fmt.Errorf("read console output code page: %w", err)
	}
	if modes.outputMode, err = modes.ops.outputMode(); err != nil {
		return fmt.Errorf("read console output mode: %w", err)
	}
	if modes.inputCP != consoleUTF8 {
		modes.inputChanged = true
		if err := modes.ops.setInputCP(consoleUTF8); err != nil {
			return fmt.Errorf("select UTF-8 console input: %w", err)
		}
	}
	if modes.outputCP != consoleUTF8 {
		modes.outputChanged = true
		if err := modes.ops.setOutputCP(consoleUTF8); err != nil {
			return fmt.Errorf("select UTF-8 console output: %w", err)
		}
	}
	target := modes.outputMode | consoleProcessedOutput | consoleVTOutput
	// Drawing escapes can change native mode bits (for example autowrap)
	// even when UTF-8/VT were already enabled. Always restore the captured
	// output mode after drawing, not just modes changed by a setup setter.
	modes.modeChanged = true
	if modes.outputMode != target {
		if err := modes.ops.setOutputMode(target); err != nil {
			return fmt.Errorf("enable console terminal drawing: %w", err)
		}
	}
	return nil
}

func (modes *consoleModes) Restore() error {
	var result error
	if modes.modeChanged {
		if err := modes.ops.setOutputMode(modes.outputMode); err != nil {
			result = errors.Join(result, fmt.Errorf("restore console output mode: %w", err))
		} else {
			modes.modeChanged = false
		}
	}
	if modes.outputChanged {
		if err := modes.ops.setOutputCP(modes.outputCP); err != nil {
			result = errors.Join(result, fmt.Errorf("restore console output code page: %w", err))
		} else {
			modes.outputChanged = false
		}
	}
	if modes.inputChanged {
		if err := modes.ops.setInputCP(modes.inputCP); err != nil {
			result = errors.Join(result, fmt.Errorf("restore console input code page: %w", err))
		} else {
			modes.inputChanged = false
		}
	}
	return result
}

// PrepareRestore permits retrying an interrupted restore-sequence write after
// the prior attempt returned native modes to their original values. Original
// values are never recaptured here, even if a prior native restore failed.
func (modes *consoleModes) PrepareRestore() error {
	modes.modeChanged = true
	currentCP, err := modes.ops.outputCP()
	if err != nil {
		return fmt.Errorf("read console output code page for restoration: %w", err)
	}
	if currentCP != consoleUTF8 {
		modes.outputChanged = true
		if err := modes.ops.setOutputCP(consoleUTF8); err != nil {
			return fmt.Errorf("select console encoding for restoration: %w", err)
		}
	}
	currentMode, err := modes.ops.outputMode()
	if err != nil {
		return fmt.Errorf("read console drawing mode for restoration: %w", err)
	}
	target := modes.outputMode | consoleProcessedOutput | consoleVTOutput
	if currentMode != target {
		modes.modeChanged = true
		if err := modes.ops.setOutputMode(target); err != nil {
			return fmt.Errorf("enable console drawing for restoration: %w", err)
		}
	}
	return nil
}
