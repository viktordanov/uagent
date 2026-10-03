package procstart_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func currentBoot(t *testing.T) string {
	t.Helper()
	id, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	require.NoError(t, err)

	return strings.TrimSpace(string(id))
}

// checkStartedNow converts the start time, in clock ticks after boot, to a
// wall time with the boot time from /proc/stat. USER_HZ is 100 on every
// Linux architecture Go supports.
func checkStartedNow(t *testing.T, _ int, identity string) {
	t.Helper()
	_, start, _ := strings.Cut(identity, "/")
	ticks, err := strconv.ParseInt(start, 10, 64)
	require.NoError(t, err, "start %q is not clock ticks", start)
	stat, err := os.ReadFile("/proc/stat")
	require.NoError(t, err)
	var btime int64
	for line := range strings.Lines(string(stat)) {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			btime, err = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			require.NoError(t, err)
		}
	}
	started := time.Unix(btime, 0).Add(time.Duration(ticks) * time.Second / 100)
	assert.WithinDuration(t, time.Now(), started, time.Minute)
}
