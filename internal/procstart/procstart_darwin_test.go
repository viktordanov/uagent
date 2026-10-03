package procstart_test

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func currentBoot(t *testing.T) string {
	t.Helper()
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	require.NoError(t, err)

	return boot
}

// checkStartedNow checks the start time against the clock and against ps,
// which reads it through another interface.
func checkStartedNow(t *testing.T, pid int, identity string) {
	t.Helper()
	_, start, _ := strings.Cut(identity, "/")
	sec, _, ok := strings.Cut(start, ".")
	require.True(t, ok, "start %q is not <seconds>.<microseconds>", start)
	n, err := strconv.ParseInt(sec, 10, 64)
	require.NoError(t, err)
	started := time.Unix(n, 0)
	assert.WithinDuration(t, time.Now(), started, time.Minute)
	out, err := exec.Command("/bin/ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	require.NoError(t, err)
	assert.Equal(t, started.Format("Mon Jan 2 15:04:05 2006"), strings.Join(strings.Fields(string(out)), " "), "ps start time")
}
