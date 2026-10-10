//go:build soak && darwin

package soak

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
)

func statusViewer(start time.Time) *viewer {
	return &viewer{session: protocol.BrokerRPCSessionRecord{CreatedAt: start.Format(time.RFC3339Nano)},
		echoWaiters: map[string]chan time.Time{}}
}

func TestObserveStatusRequiresCompleteRecord(t *testing.T) {
	written := time.Unix(1791350100, 0)
	received := written.Add(15 * time.Millisecond)
	record := fmt.Sprintf("S pid=123 seq=45 echo=P0x1 t=%d !", written.UnixNano())
	for split := 0; split < len(record); split++ {
		v := statusViewer(written.Add(-time.Second))
		waiter := make(chan time.Time, 1)
		v.echoWaiters["P0x1"] = waiter
		v.screen = []string{record[:split]}
		v.observeStatus(received)
		if len(v.outputLatency) != 0 || v.pid != 0 || v.seq != 0 || v.echo != "" || len(waiter) != 0 {
			t.Fatalf("split %d accepted an incomplete record: pid=%d seq=%d echo=%q samples=%v", split, v.pid, v.seq, v.echo, v.outputLatency)
		}
		v.screen[0] = record
		v.observeStatus(received)
		if len(v.outputLatency) != 1 || v.outputLatency[0] != 15*time.Millisecond || len(waiter) != 1 {
			t.Fatalf("split %d complete record: samples=%v echo notifications=%d", split, v.outputLatency, len(waiter))
		}
	}
}

func TestObserveStatusSamplesEachWriteOnce(t *testing.T) {
	written := time.Unix(1791350100, 0)
	v := statusViewer(written.Add(-time.Second))
	v.screen = []string{fmt.Sprintf("S pid=123 seq=0 echo=- t=%d !", written.UnixNano())}
	v.observeStatus(written.Add(15 * time.Millisecond))
	v.observeStatus(written.Add(time.Second)) // unrelated row patch
	if samples := v.takeLatencies(); len(samples) != 1 || samples[0] != 15*time.Millisecond {
		t.Fatalf("unchanged status sampled again: %v", samples)
	}
	v.observeStatus(written.Add(2 * time.Second)) // collecting does not reset identity
	if samples := v.takeLatencies(); len(samples) != 0 {
		t.Fatalf("collected status sampled again: %v", samples)
	}
	// A paused child still writes timestamps even when its flood sequence stays zero.
	written = written.Add(10 * time.Millisecond)
	v.screen[0] = fmt.Sprintf("S pid=123 seq=0 echo=PAUSE t=%d !", written.UnixNano())
	v.observeStatus(written.Add(20 * time.Millisecond))
	if samples := v.takeLatencies(); len(samples) != 1 || samples[0] != 20*time.Millisecond || v.echo != "PAUSE" {
		t.Fatalf("fresh paused status not sampled: %v echo=%q", samples, v.echo)
	}
}

func TestObserveStatusKeepsSlowLinkSamples(t *testing.T) {
	written := time.Unix(1791350100, 0)
	v := statusViewer(written.Add(-time.Second))
	v.screen = []string{fmt.Sprintf("S pid=123 seq=45 echo=- t=%d !", written.UnixNano())}
	v.observeStatus(written.Add(time.Hour))
	if samples := v.takeLatencies(); len(samples) != 1 || samples[0] != time.Hour {
		t.Fatalf("valid delayed record was filtered: %v", samples)
	}
}

func TestObserveStatusRejectsInvalidRecords(t *testing.T) {
	written := time.Unix(1791350100, 0)
	valid := fmt.Sprintf("S pid=123 seq=45 echo=P0x1 t=%d !", written.UnixNano())
	cases := []string{
		"S pid=123 seq=45 echo=P0x1 t=179 !",
		strings.Replace(valid, "S pid=", "X pid=", 1),
		strings.Replace(valid, "pid=123", "pid=0", 1),
		strings.Replace(valid, "seq=45", "seq=-1", 1),
		strings.Replace(valid, "seq=45", "seq=bad", 1),
		strings.Replace(valid, "echo=P0x1", "echo=", 1),
		strings.Replace(valid, "t=1791350100000000000", "t=1791350101000000000", 1), // future
		strings.Replace(valid, "t=1791350100000000000", "t=9223372036854775808", 1), // overflow
		strings.Replace(valid, "pid=123", "pid=+123", 1),
		valid + " extra",
		strings.Replace(valid, "seq=45", "pid=45", 1), // duplicate key
	}
	for _, record := range cases {
		v := statusViewer(written.Add(-time.Second))
		v.screen = []string{record}
		v.observeStatus(written.Add(15 * time.Millisecond))
		if len(v.outputLatency) != 0 || v.pid != 0 || v.seq != 0 || v.echo != "" {
			t.Errorf("accepted invalid record %q: samples=%v pid=%d seq=%d echo=%q", record, v.outputLatency, v.pid, v.seq, v.echo)
		}
	}
	for _, record := range []string{
		fmt.Sprintf("S pid=123 seq=45 echo=P0x1 t=%d !", written.Add(-time.Second).UnixNano()),
		fmt.Sprintf("S pid=124 seq=45 echo=P0x1 t=%d !", written.Add(time.Millisecond).UnixNano()),
		fmt.Sprintf("S pid=123 seq=44 echo=P0x1 t=%d !", written.Add(time.Millisecond).UnixNano()),
	} {
		v := statusViewer(written.Add(-time.Second))
		v.screen = []string{valid}
		v.observeStatus(written.Add(15 * time.Millisecond))
		v.screen[0] = record
		v.observeStatus(written.Add(20 * time.Millisecond))
		if len(v.outputLatency) != 1 || v.pid != 123 || v.seq != 45 {
			t.Errorf("accepted stale/inconsistent record %q: samples=%v pid=%d seq=%d", record, v.outputLatency, v.pid, v.seq)
		}
	}
}

func TestObserveStatusEveryPTYWriteSplit(t *testing.T) {
	written := time.Unix(1791350100, 0)
	received := written.Add(15 * time.Millisecond)
	output := fmt.Sprintf(childStatusFormat, 123, 45, "P0x1", written.UnixNano())
	complete := strings.Index(output, "!") + 1
	for split := 0; split <= len(output); split++ {
		core, err := terminal.New(context.Background(), terminal.Config{Columns: 80, Rows: childRows})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			for range core.Output() {
			}
		}()
		v := statusViewer(written.Add(-time.Second))
		observe := func() {
			frame, err := core.ScreenSnapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var row strings.Builder
			for _, cell := range frame.Screen[0].Cells {
				row.WriteString(cell.Text)
			}
			v.screen = []string{row.String()}
			v.observeStatus(received)
		}
		if err := core.WriteOutput(context.Background(), []byte(output[:split])); err != nil {
			t.Fatal(err)
		}
		observe()
		if split < complete && len(v.outputLatency) != 0 {
			t.Fatalf("PTY split %d before end marker sampled %v", split, v.outputLatency)
		}
		if err := core.WriteOutput(context.Background(), []byte(output[split:])); err != nil {
			t.Fatal(err)
		}
		observe()
		_ = core.Close()
		if len(v.outputLatency) != 1 || v.outputLatency[0] != 15*time.Millisecond {
			t.Fatalf("PTY split %d complete status sampled %v", split, v.outputLatency)
		}
	}
}
