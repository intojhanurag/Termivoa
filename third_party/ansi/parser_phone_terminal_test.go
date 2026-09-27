package ansi

import (
	"reflect"
	"testing"

	"github.com/charmbracelet/x/ansi/parser"
)

func TestPhoneTerminalUTF8StringsKeepC1ContinuationBytes(t *testing.T) {
	cases := []testCase{
		{
			// U+201C and U+201D end in 0x9C and 0x9D; U+2733 contains 0x9C.
			name:  "osc_bel",
			input: "abc\x1b]0;say \xe2\x80\x9chi\xe2\x80\x9d \xe2\x9c\xb3\x07def",
			expected: []any{
				'a', 'b', 'c',
				[]byte("0;say \xe2\x80\x9chi\xe2\x80\x9d \xe2\x9c\xb3"),
				'd', 'e', 'f',
			},
		},
		{
			name:  "osc_esc_st",
			input: "\x1b]2;\xe6\x9c\xab\x1b\\x",
			expected: []any{
				[]byte("2;\xe6\x9c\xab"),
				Cmd('\\'),
				'x',
			},
		},
		{
			// C1 values as continuation bytes, including the CSI (0x9B),
			// OSC (0x9D), and APC (0x9F) introducers.
			name:  "osc_c1_continuations",
			input: "\x1b]2;\xc4\x80\xc4\x98\xc4\x9b\xc4\x9c\xc4\x9d\xc4\x9f\x07x",
			expected: []any{
				[]byte("2;\xc4\x80\xc4\x98\xc4\x9b\xc4\x9c\xc4\x9d\xc4\x9f"),
				'x',
			},
		},
		{
			name:  "dcs",
			input: "\x1bP1$q\xe2\x80\x9c\x1b\\x",
			expected: []any{
				dcsSequence{Cmd: 'q' | '$'<<parser.IntermedShift, Params: Params{1}, Data: []byte("\xe2\x80\x9c")},
				Cmd('\\'),
				'x',
			},
		},
		{
			name:  "apc",
			input: "\x1b_\xe2\x9c\xb3\xe2\x80\x9c\x1b\\x",
			expected: []any{
				[]byte("\xe2\x9c\xb3\xe2\x80\x9c"),
				Cmd('\\'),
				'x',
			},
		},
		{
			name:     "can_still_cancels",
			input:    "\x1b]0;\xe2\x80\x9c\x18x",
			expected: []any{'x'},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dispatcher := &testDispatcher{}
			p := testParser(dispatcher)
			p.SetUTF8Strings(true)
			p.Parse([]byte(c.input))
			if !reflect.DeepEqual(dispatcher.dispatched, c.expected) {
				t.Fatalf("dispatched %#v, want %#v", dispatcher.dispatched, c.expected)
			}
		})
	}
}

func TestPhoneTerminalUTF8StringsDisabledKeepsUpstreamC1ST(t *testing.T) {
	dispatcher := &testDispatcher{}
	p := testParser(dispatcher)
	p.Parse([]byte("\x1b]0;a\x9cb"))
	want := []any{[]byte("0;a"), 'b'}
	if !reflect.DeepEqual(dispatcher.dispatched, want) {
		t.Fatalf("dispatched %#v, want %#v", dispatcher.dispatched, want)
	}
}
