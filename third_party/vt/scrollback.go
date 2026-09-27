package vt

import (
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultScrollbackSize is the default maximum number of lines in the scrollback buffer.
const DefaultScrollbackSize = 10000

// normalizedCellBytes conservatively covers the fixed fields and style data
// in Termivoa's normalized cell representation. Variable display/link
// strings are charged separately below.
const normalizedCellBytes = 256

// Scrollback represents a scrollback buffer that stores lines scrolled off the screen.
type Scrollback struct {
	lines     []uv.Line
	maxLines  int
	bytes     int
	maxBytes  int
	cellWidth int
	onChange  func(reset bool)
}

// NewScrollback creates a new scrollback buffer with the given maximum number of lines.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{
		lines:    make([]uv.Line, 0, min(maxLines, 1000)), // Pre-allocate reasonable capacity
		maxLines: maxLines,
	}
}

// Push adds a line to the scrollback buffer.
// If the buffer is full, the oldest line is removed.
func (s *Scrollback) Push(line uv.Line) {
	if s == nil || s.maxLines <= 0 {
		return
	}

	// Find last non-empty cell to trim trailing empty cells.
	// This helps with wrapping and window resizing.
	lastNonEmpty := -1
	for i := len(line) - 1; i >= 0; i-- {
		c := &line[i]
		if !c.IsZero() && !c.Equal(&uv.EmptyCell) {
			lastNonEmpty = i
			break
		}
	}

	// Clone the line content up to and including the last non-empty cell
	cloned := slices.Clone(line[:lastNonEmpty+1])

	if len(s.lines) >= s.maxLines {
		// Remove oldest line and append new one
		s.bytes -= s.normalizedLineBytes(s.lines[0])
		s.lines = slices.Delete(s.lines, 0, 1)
	}
	s.lines = append(s.lines, cloned)
	s.bytes += s.normalizedLineBytes(cloned)
	s.enforceByteLimit()
	s.changed(false)
}

// PushN adds n lines from the buffer starting at line y to the scrollback.
func (s *Scrollback) PushN(buf *uv.RenderBuffer, y, n int) {
	if s == nil || buf == nil || n <= 0 {
		return
	}

	for i := range min(n, buf.Height()-y) {
		if line := buf.Line(y + i); line != nil {
			s.Push(line)
		}
	}
}

// Len returns the number of lines in the scrollback buffer.
func (s *Scrollback) Len() int {
	if s == nil {
		return 0
	}
	return len(s.lines)
}

// MaxLines returns the maximum number of lines the scrollback buffer can hold.
func (s *Scrollback) MaxLines() int {
	if s == nil {
		return 0
	}
	return s.maxLines
}

// SetMaxLines sets the maximum number of lines in the scrollback buffer.
// If the current number of lines exceeds the new maximum, oldest lines are removed.
func (s *Scrollback) SetMaxLines(maxLines int) {
	if s == nil || maxLines <= 0 {
		return
	}

	s.maxLines = maxLines
	if len(s.lines) > maxLines {
		for _, line := range s.lines[:len(s.lines)-maxLines] {
			s.bytes -= s.normalizedLineBytes(line)
		}
		// Remove oldest lines
		s.lines = s.lines[len(s.lines)-maxLines:]
	}
}

// SetMaxBytes sets the maximum normalized byte estimate retained in
// scrollback. A value of zero disables the byte limit.
func (s *Scrollback) SetMaxBytes(maxBytes int) {
	if s == nil || maxBytes < 0 {
		return
	}
	s.maxBytes = maxBytes
	s.enforceByteLimit()
}

// SetCellWidth updates the number of normalized cells charged for every
// retained line. History readers pad short stored lines to this width, so the
// scrollback allocation must account for those cells too.
func (s *Scrollback) SetCellWidth(width int) {
	if s == nil || width < 0 || width == s.cellWidth {
		return
	}
	s.cellWidth = width
	s.recalculateBytes()
	before := len(s.lines)
	s.enforceByteLimit()
	if len(s.lines) != before {
		s.changed(false)
	}
}

// SetChangeCallback reports main-screen history mutation. reset is true only
// for an explicit history clear, not ordinary oldest-row eviction.
func (s *Scrollback) SetChangeCallback(callback func(reset bool)) {
	if s == nil {
		return
	}
	s.onChange = callback
}

// Bytes returns the conservative normalized payload estimate currently held.
func (s *Scrollback) Bytes() int {
	if s == nil {
		return 0
	}
	return s.bytes
}

func (s *Scrollback) enforceByteLimit() {
	if s == nil || s.maxBytes == 0 {
		return
	}
	for len(s.lines) > 0 && s.bytes > s.maxBytes {
		s.bytes -= s.normalizedLineBytes(s.lines[0])
		s.lines = slices.Delete(s.lines, 0, 1)
	}
}

func (s *Scrollback) normalizedLineBytes(line uv.Line) int {
	size := max(len(line), s.cellWidth) * normalizedCellBytes
	for i := range line {
		size += len(line[i].Content) + len(line[i].Link.URL)
	}
	return size
}

func (s *Scrollback) recalculateBytes() {
	s.bytes = 0
	for _, line := range s.lines {
		s.bytes += s.normalizedLineBytes(line)
	}
}

func (s *Scrollback) changed(reset bool) {
	if s.onChange != nil {
		s.onChange(reset)
	}
}

// Line returns the line at the given index.
// Index 0 is the oldest line, Len()-1 is the most recent.
// Returns nil if index is out of bounds.
func (s *Scrollback) Line(index int) uv.Line {
	if s == nil || index < 0 || index >= len(s.lines) {
		return nil
	}
	return s.lines[index]
}

// Lines returns all lines in the scrollback buffer.
// Index 0 is the oldest line.
func (s *Scrollback) Lines() []uv.Line {
	if s == nil {
		return nil
	}
	return s.lines
}

// Clear removes all lines from the scrollback buffer.
func (s *Scrollback) Clear() {
	if s == nil {
		return
	}
	s.lines = s.lines[:0]
	s.bytes = 0
	s.changed(true)
}

// CellAt returns the cell at the given position in the scrollback buffer.
// x is the column, y is the line index (0 = oldest).
// Returns nil if position is out of bounds.
func (s *Scrollback) CellAt(x, y int) *uv.Cell {
	line := s.Line(y)
	if line == nil || x < 0 || x >= len(line) {
		return nil
	}
	return &line[x]
}
