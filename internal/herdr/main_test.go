package herdr

import (
	"os"
	"testing"
	"time"
)

// TestMain gives herdr CLI runs a long limit in tests: under a loaded
// parallel run the first `terminal attach --help` of a script could pass the
// real 3 s and be read as "cannot attach". A test of the limit sets its own.
func TestMain(m *testing.M) {
	commandTimeout = 30 * time.Second
	os.Exit(m.Run())
}
