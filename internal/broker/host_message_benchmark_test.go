package broker

import (
	"fmt"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func BenchmarkHostTerminalMessage(b *testing.B) {
	for _, columns := range []int{80, 200, 400} {
		b.Run(fmt.Sprint(columns), func(b *testing.B) {
			frame := viewerFrame(2, columns, 24, "x")
			changed := make([]terminal.ChangedRow, len(frame.Screen))
			for i, row := range frame.Screen {
				changed[i] = terminal.ChangedRow{Index: i, Row: row}
			}
			payload, err := protocol.PatchPayload(terminal.Patch{
				BaseRevision: 1, Revision: 2, Columns: columns, Rows: 24,
				WidthMethod: frame.WidthMethod, Foreground: frame.Foreground,
				Background: frame.Background, CursorColor: frame.CursorColor,
				Cursor: frame.Cursor, ChangedRows: changed,
			}, false)
			if err != nil {
				b.Fatal(err)
			}
			subscription := ViewerSubscription{session: &Session{id: testSessionID, generation: testGeneration}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := subscription.hostMessageForRequest(protocol.ScreenPatch, payload, testViewerConnection); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
