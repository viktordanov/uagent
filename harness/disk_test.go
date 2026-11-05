package harness

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/core"
)

// writeOperationsDir fills dir like the largest session operation directory
// measured in a real uah state directory: 18 operations with captured
// output (36 files) beside a $TMPDIR holding 17,467 Go build cache files.
func writeOperationsDir(tb testing.TB, dir string) {
	tb.Helper()
	write := func(path string, size int) {
		require.NoError(tb, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(tb, os.WriteFile(path, make([]byte, size), 0o600))
	}
	for i := range 18 {
		op := filepath.Join(dir, fmt.Sprintf("%08x-06cc-4f35-b025-fc5edc5ae046", i))
		write(filepath.Join(op, "out"), 16<<10)
		write(filepath.Join(op, "err"), 100)
	}
	for i := range 17467 {
		write(filepath.Join(dir, "tmp", "go-build", fmt.Sprintf("%02x", i%256), fmt.Sprintf("%016x-d", i)), 2<<10)
	}
}

func BenchmarkDirSize(b *testing.B) {
	dir := b.TempDir()
	writeOperationsDir(b, dir)
	for b.Loop() {
		dirSize(dir, []string{"tmp", "review-tmp"})
	}
}

// stoppableAgent stops as soon as it is asked to.
type stoppableAgent struct {
	done chan struct{}
	once sync.Once
}

func (a *stoppableAgent) Done() <-chan struct{} { return a.done }
func (a *stoppableAgent) ExitCode() int         { return 130 }
func (a *stoppableAgent) Interrupt()            { a.once.Do(func() { close(a.done) }) }
func (a *stoppableAgent) Terminate()            { a.Interrupt() }
func (a *stoppableAgent) Kill()                 { a.Interrupt() }

// TestSupervise_InterruptDuringADiskScan: a scan of the operations directory
// that has not finished does not hold up an interrupt.
func TestSupervise_InterruptDuringADiskScan(t *testing.T) {
	SetDiskPollInterval(t, time.Millisecond)
	scanning, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var started sync.Once
	p := &process{
		h:     &Harness{cfg: Config{MaxDisk: 1, KillGrace: time.Second}},
		agent: &stoppableAgent{done: make(chan struct{})},
		log:   slog.New(slog.DiscardHandler),
		diskUsage: func() int64 {
			started.Do(func() { close(scanning) })
			<-release

			return 0
		},
	}
	interrupt := make(chan struct{})
	ended := make(chan core.Termination, 1)
	go func() { ended <- p.supervise(context.Background(), interrupt, nil) }()
	<-scanning

	close(interrupt)

	select {
	case got := <-ended:
		assert.Equal(t, core.TerminationInterrupted, got)
	case <-time.After(5 * time.Second):
		t.Fatal("the interrupt waited for the disk scan")
	}
}
