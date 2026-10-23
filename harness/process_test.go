package harness

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/internal/procstart"
)

// liveGroup starts a sleeper in its own process group and returns its ID
// and its leader's start.
func liveGroup(t *testing.T) (int, string) {
	t.Helper()
	sleeper := exec.Command("/bin/sleep", "30")
	sleeper.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, sleeper.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-sleeper.Process.Pid, syscall.SIGKILL)
		_ = sleeper.Wait()
	})
	start, err := procstart.Of(sleeper.Process.Pid)
	require.NoError(t, err)

	return sleeper.Process.Pid, start
}

func operationLine(id, status string, pgid int, start string) string {
	return fmt.Sprintf(`{"type":"operation","data":{"Operation":{"ID":%q,"Status":%q,"State":{"ProcessGroupID":%d,"ProcessGroupStart":%q}}}}`,
		id, status, pgid, start)
}

// itemLine is an item record of about size bytes whose text names an
// operation record, as a tool's output can.
func itemLine(seq, size int) string {
	text := strings.Repeat("x", max(size-200, 0))

	return fmt.Sprintf(`{"type":"item","data":{"Item":{"Sequence":%d,"Kind":"tool_output","Data":{"Output":"{\"type\":\"operation\" %s"}}}}`, seq, text)
}

func TestLiveOperationGroups(t *testing.T) {
	running, runningStart := liveGroup(t)
	reordered, reorderedStart := liveGroup(t)
	done, doneStart := liveGroup(t)
	lines := []string{
		`{"type":"session","data":{"Version":2,"Session":{"ID":"s"}}}`,
		itemLine(1, 4096),
		operationLine("op-1", "running", running, runningStart),
		itemLine(2, 4096),
		// Written in another field order: decoded all the same.
		fmt.Sprintf(`{"data":{"Operation":{"ID":"op-2","Status":"running","State":{"ProcessGroupID":%d,"ProcessGroupStart":%q}}},"type":"operation"}`,
			reordered, reorderedStart),
		operationLine("op-3", "running", done, doneStart),
		operationLine("op-3", "completed", 0, ""),
	}
	path := filepath.Join(t.TempDir(), "s.session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	got := liveOperationGroups(path, slog.New(slog.NewTextHandler(io.Discard, nil)))

	assert.ElementsMatch(t, []int{running, reordered}, got, "the running operations' groups, not the completed one's")
}

// BenchmarkLiveOperationGroups scans a 40 MB session file: 2,000 items of
// 20 KB and 200 finished operations, as a long session holds.
func BenchmarkLiveOperationGroups(b *testing.B) {
	var sb strings.Builder
	for i := range 2000 {
		sb.WriteString(itemLine(i, 20<<10) + "\n")
		if i%10 == 0 {
			id := fmt.Sprintf("op-%d", i)
			sb.WriteString(operationLine(id, "running", 4242, "boot/1") + "\n")
			sb.WriteString(operationLine(id, "completed", 0, "") + "\n")
		}
	}
	path := filepath.Join(b.TempDir(), "s.session.jsonl")
	require.NoError(b, os.WriteFile(path, []byte(sb.String()), 0o600))
	b.SetBytes(int64(sb.Len()))
	b.ReportAllocs()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for b.Loop() {
		liveOperationGroups(path, log)
	}
}
