package harness

import (
	"testing"
	"time"
)

// SetDiskPollInterval shortens how often runs measure their tool output,
// until the test ends.
func SetDiskPollInterval(tb testing.TB, d time.Duration) {
	tb.Helper()
	old := diskPollInterval
	diskPollInterval = d
	tb.Cleanup(func() { diskPollInterval = old })
}
