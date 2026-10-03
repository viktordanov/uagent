//go:build darwin || linux

package procstart_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/internal/procstart"
)

func TestOf_IsStableForALiveProcess(t *testing.T) {
	first, err := procstart.Of(os.Getpid())
	require.NoError(t, err)
	second, err := procstart.Of(os.Getpid())
	require.NoError(t, err)
	assert.Equal(t, first, second)
	boot, start, ok := strings.Cut(first, "/")
	require.True(t, ok, "<boot>/<start>: %q", first)
	assert.Equal(t, currentBoot(t), boot)
	assert.NotEmpty(t, start)
}

func TestOf_TellsProcessesApart(t *testing.T) {
	self, err := procstart.Of(os.Getpid())
	require.NoError(t, err)
	// Linux counts start times in clock ticks (10 ms): a process ID cannot be
	// reused that fast, but a child started at once can share its parent's tick.
	time.Sleep(30 * time.Millisecond)
	child := exec.Command("/bin/sleep", "30")
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	other, err := procstart.Of(child.Process.Pid)
	require.NoError(t, err)
	assert.NotEqual(t, self, other)
	selfBoot, _, _ := strings.Cut(self, "/")
	otherBoot, _, _ := strings.Cut(other, "/")
	assert.Equal(t, selfBoot, otherBoot)
	checkStartedNow(t, child.Process.Pid, other)
}

func TestOf_GoneProcess(t *testing.T) {
	child := exec.Command("/bin/sh", "-c", "exit 0")
	require.NoError(t, child.Run())
	_, err := procstart.Of(child.Process.Pid)
	require.ErrorIs(t, err, procstart.ErrNoProcess)
	_, err = procstart.Of(0)
	require.ErrorIs(t, err, procstart.ErrNoProcess)
}
