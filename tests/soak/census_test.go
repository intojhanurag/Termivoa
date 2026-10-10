//go:build soak && darwin

package soak

import (
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestSoakFixtureChildCensusSnapshot(t *testing.T) {
	const fixtures = `101 90 /tmp/soak.test -test.run=^TestSoakChild$ -- pterm-soak-child 65536
102 90 /tmp/soak.test -test.run=^TestSoakChild$ -- pterm-soak-child 65536
103 90 /tmp/soak.test -test.run=^TestSoakChild$ -- pterm-soak-child 65536
`
	for _, tc := range []struct {
		name, snapshot string
		want           []int
	}{
		{"scanner overlaps census", fixtures + "104 90 /usr/sbin/lsof -a -p 101 -d cwd -Fn\n105 90 (soak.test)\n", []int{101, 102, 103}},
		{"unexpected duplicate fixture", fixtures + "999 90 /tmp/soak.test -test.run=^TestSoakChild$ -- pterm-soak-child 65536\n", []int{101, 102, 103, 999}},
		{"vanished scanner helper", fixtures, []int{101, 102, 103}},
		{"missing fixture", "101 90 /tmp/soak.test -- pterm-soak-child 0\n102 90 /tmp/soak.test -- pterm-soak-child 0\n", []int{101, 102}},
		{"other parent and marker substrings", fixtures + "104 91 /tmp/soak.test -- pterm-soak-child 0\n105 90 /tmp/pterm-soak-child -- helper 0\n106 90 /tmp/soak.test -- prefix-pterm-soak-child 0\n", []int{101, 102, 103}},
		{"duplicate with unexpected arguments", fixtures + "999 90 /tmp/soak.test -- pterm-soak-child invalid extra\n", []int{101, 102, 103, 999}},
		{"executable path with spaces", "101 90 /tmp/test files/soak.test -- pterm-soak-child 0\n", []int{101}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixtureChildPIDsFromSnapshot(90, tc.snapshot); !slices.Equal(got, tc.want) {
				t.Fatalf("fixture PIDs = %v, want %v", got, tc.want)
			}
		})
	}
}

// Exercise the real ps snapshot with a repeating lsof helper and two actual
// terminal fixtures. No expected-PID allowlist is passed to the census.
func TestSoakFixtureChildCensusLive(t *testing.T) {
	parent := os.Getpid()
	startFixture := func() (*exec.Cmd, func()) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestSoakChild$", "--", childMarker, "0")
		terminal, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		drained := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, terminal)
			close(drained)
		}()
		stop := func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				_ = terminal.Close()
				<-drained
			}
		}
		t.Cleanup(stop)
		return cmd, stop
	}
	first, stopFirst := startFixture()
	waitFor(t, 5*time.Second, "first census fixture", func() bool {
		return slices.Equal(fixtureChildPIDs(t, parent), []int{first.Process.Pid})
	})

	scanner := exec.Command("lsof", "-r1", "-p", strconv.Itoa(parent))
	scanner.Stdout, scanner.Stderr = io.Discard, io.Discard
	if err := scanner.Start(); err != nil {
		t.Fatal(err)
	}
	stopScanner := func() {
		if scanner.ProcessState == nil {
			_ = scanner.Process.Kill()
			_ = scanner.Wait()
		}
	}
	t.Cleanup(stopScanner)
	waitFor(t, 5*time.Second, "scanner helper running", func() bool {
		out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(scanner.Process.Pid)).Output()
		return err == nil && strings.HasSuffix(strings.TrimSpace(string(out)), "lsof")
	})
	if got := fixtureChildPIDs(t, parent); !processAlive(scanner.Process.Pid) || !slices.Equal(got, []int{first.Process.Pid}) {
		t.Fatalf("overlapping scanner changed fixture census: %v", got)
	}

	second, _ := startFixture()
	waitFor(t, 5*time.Second, "unexpected duplicate fixture", func() bool {
		got := fixtureChildPIDs(t, parent)
		return len(got) == 2 && slices.Contains(got, first.Process.Pid) && slices.Contains(got, second.Process.Pid)
	})
	stopScanner()
	if got := fixtureChildPIDs(t, parent); len(got) != 2 {
		t.Fatalf("vanished scanner changed fixture census: %v", got)
	}
	stopFirst()
	if got := fixtureChildPIDs(t, parent); !slices.Equal(got, []int{second.Process.Pid}) {
		t.Fatalf("missing fixture was not reflected in census: %v", got)
	}
}
