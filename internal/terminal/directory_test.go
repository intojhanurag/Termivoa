package terminal

import (
	"strings"
	"testing"
)

func TestCoreRecordsDirectoryReports(t *testing.T) {
	t.Parallel()
	core := newTestCore(t, 20, 5)
	if got := core.DirectoryReport(); got != (DirectoryReport{}) {
		t.Fatalf("initial report = %+v", got)
	}
	// OSC 7 split across reads, BEL-terminated.
	mustWrite(t, core, "a\x1b]7;file://host/Us")
	mustWrite(t, core, "ers/me\x07b")
	if got := core.DirectoryReport(); got != (DirectoryReport{Payload: "7;file://host/Users/me", Seq: 1}) {
		t.Fatalf("OSC 7 report = %+v", got)
	}
	// OSC 9;9, ST-terminated; the same path again still counts as fresh.
	mustWrite(t, core, "\x1b]9;9;\"C:\\dir\"\x1b\\")
	mustWrite(t, core, "\x1b]9;9;\"C:\\dir\"\x1b\\")
	if got := core.DirectoryReport(); got != (DirectoryReport{Payload: "9;9;\"C:\\dir\"", Seq: 3}) {
		t.Fatalf("OSC 9;9 report = %+v", got)
	}
	// Notifications (OSC 9;text), other OSCs and oversized reports are ignored.
	mustWrite(t, core, "\x1b]9;build done\x07\x1b]2;title\x07\x1b]7;file:///"+strings.Repeat("a", MaxDirectoryReportBytes)+"\x07")
	if got := core.DirectoryReport(); got.Seq != 3 {
		t.Fatalf("ignored sequences changed the report: %+v", got)
	}
}
