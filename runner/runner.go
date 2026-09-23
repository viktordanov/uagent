// Package runner drives unreal-agent-runner as a subprocess and turns its
// JSONL output into domain events.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/statedir"
)

// BinaryName is the runner executable uagent looks for.
const BinaryName = "unreal-agent-runner"

const (
	defaultKillGrace = 5 * time.Second
	drainTimeout     = 2 * time.Second
	diskPollInterval = 5 * time.Second
)

// Config configures the subprocess runner.
type Config struct {
	Bin    string
	Layout statedir.Layout
	// MaxDisk kills the run when the session's tool output exceeds it (0 disables).
	MaxDisk int64
	// KillGrace is the wait between SIGTERM and SIGKILL (default 5s).
	KillGrace time.Duration
	Logger    *slog.Logger
}

type runner struct {
	cfg Config
	log *slog.Logger
}

func New(cfg Config) domain.Runner {
	if cfg.KillGrace <= 0 {
		cfg.KillGrace = defaultKillGrace
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &runner{cfg: cfg, log: logger.WithGroup("runner")}
}

// Resolve finds the runner: an explicit path, then ~/.local/bin, then PATH.
func Resolve(explicit string) (string, error) {
	if explicit != "" {
		if !isExecutable(explicit) {
			return "", fmt.Errorf("failed to use runner %q: not an executable file", explicit)
		}

		return explicit, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, ".local", "bin", BinaryName); isExecutable(p) {
			return p, nil
		}
	}
	p, err := exec.LookPath(BinaryName)
	if err != nil {
		return "", fmt.Errorf("failed to find %s (install: GOBIN=\"$HOME/.local/bin\" go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-runner@latest): %w", BinaryName, err)
	}

	return p, nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func (r *runner) Run(ctx context.Context, req domain.RunRequest, emit func(domain.Event)) (domain.RunnerExit, error) {
	layout := r.cfg.Layout
	runDir := layout.RunDir(req.RunID)
	for _, dir := range []string{runDir, layout.SessionsDir(), layout.LogsDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return domain.RunnerExit{}, fmt.Errorf("failed to create state directory: %w", err)
		}
	}
	request, err := json.Marshal(requestToDTO(req))
	if err != nil {
		return domain.RunnerExit{}, fmt.Errorf("failed to encode runner request: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, statedir.RequestFile), request, 0o600); err != nil {
		return domain.RunnerExit{}, fmt.Errorf("failed to write request: %w", err)
	}
	events, err := os.Create(filepath.Join(runDir, statedir.EventsFile))
	if err != nil {
		return domain.RunnerExit{}, fmt.Errorf("failed to create events file: %w", err)
	}
	defer events.Close()
	stderr, err := os.Create(filepath.Join(runDir, statedir.StderrFile))
	if err != nil {
		return domain.RunnerExit{}, fmt.Errorf("failed to create stderr file: %w", err)
	}
	defer stderr.Close()

	// A raw pipe instead of StdoutPipe: Wait must not block on grandchildren
	// that inherited stdout, and reading stops on our terms.
	pr, pw, err := os.Pipe()
	if err != nil {
		return domain.RunnerExit{}, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	cmd := exec.Command(r.cfg.Bin, //nolint:noctx // cancellation is handled by teardown, which also kills tool process groups
		"-workspace", req.Workspace,
		"-session-directory", layout.SessionsDir(),
		"-log-directory", layout.LogsDir(),
	)
	cmd.Dir = req.Workspace
	cmd.Env = runnerEnv(req)
	cmd.Stdin = bytes.NewReader(request)
	cmd.Stdout, cmd.Stderr = pw, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()

		return domain.RunnerExit{}, fmt.Errorf("failed to start runner: %w", err)
	}
	_ = pw.Close()
	pgid := cmd.Process.Pid
	log := r.log.With(
		slog.String("run_id", req.RunID),
		slog.Int("pgid", pgid),
	)
	log.LogAttrs(ctx, slog.LevelDebug, "runner started")

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		decoder := NewDecoder()
		br := bufio.NewReaderSize(pr, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(line) > 0 {
				if _, werr := events.Write(line); werr != nil {
					log.LogAttrs(ctx, slog.LevelWarn, "event write failed", slog.String("error", werr.Error()))
				}
				for _, e := range decoder.Decode(line) {
					emit(e)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait() // the exit code is read from ProcessState below
		close(waitCh)
	}()

	termination := r.supervise(ctx, req, pgid, waitCh, log)

	select {
	case <-readDone:
	case <-time.After(drainTimeout):
		_ = pr.Close()
		<-readDone
	}
	_ = pr.Close()

	return domain.RunnerExit{
		Code:        cmd.ProcessState.ExitCode(),
		Termination: termination,
		EndedAt:     time.Now(),
	}, nil
}

// supervise waits for the runner, killing it on context end or disk overrun.
func (r *runner) supervise(ctx context.Context, req domain.RunRequest, pgid int, waitCh <-chan struct{}, log *slog.Logger) domain.Termination {
	disk := time.NewTicker(diskPollInterval)
	defer disk.Stop()
	sessionFile := r.cfg.Layout.SessionFile(req.SessionID)
	for {
		select {
		case <-waitCh:
			return domain.TerminationExited
		case <-ctx.Done():
			log.LogAttrs(ctx, slog.LevelInfo, "stopping runner", slog.String("reason", context.Cause(ctx).Error()))
			r.teardown(pgid, sessionFile, waitCh)

			return domain.TerminationCanceled
		case <-disk.C:
			if r.cfg.MaxDisk <= 0 {
				continue
			}
			if used := dirSize(r.cfg.Layout.OperationsDir(req.SessionID)); used > r.cfg.MaxDisk {
				log.LogAttrs(ctx, slog.LevelInfo, "stopping runner",
					slog.String("reason", "disk limit"),
					slog.Int64("used_bytes", used),
					slog.Int64("limit_bytes", r.cfg.MaxDisk))
				r.teardown(pgid, sessionFile, waitCh)

				return domain.TerminationDiskLimit
			}
		}
	}
}

// teardown stops the runner's process group and every still-running
// background tool group: SIGTERM, a grace period, then SIGKILL.
func (r *runner) teardown(pgid int, sessionFile string, waitCh <-chan struct{}) {
	groups := append([]int{pgid}, liveOperationGroups(sessionFile)...)
	signalGroups(groups, syscall.SIGTERM)
	select {
	case <-waitCh:
	case <-time.After(r.cfg.KillGrace):
		signalGroups(groups, syscall.SIGKILL)
		<-waitCh
	}
	// Tool groups may outlive the runner.
	signalGroups(append(groups, liveOperationGroups(sessionFile)...), syscall.SIGKILL)
}

func signalGroups(groups []int, sig syscall.Signal) {
	for _, g := range groups {
		if g > 1 {
			_ = syscall.Kill(-g, sig) // ESRCH means the group is already gone
		}
	}
}

// liveOperationGroups returns process groups of operations whose latest
// recorded status in the session file is not terminal.
func liveOperationGroups(sessionFile string) []int {
	f, err := os.Open(sessionFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	type opState struct {
		status string
		pgid   int
	}
	latest := map[string]opState{}
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		var rec sessionRecordDTO
		if json.Unmarshal(line, &rec) == nil && rec.Type == "operation" {
			op := rec.Data.Operation
			state := latest[op.ID]
			state.status = op.Status
			if op.State.ProcessGroupID != 0 {
				state.pgid = op.State.ProcessGroupID
			}
			latest[op.ID] = state
		}
		if err != nil {
			break
		}
	}
	var groups []int
	for _, s := range latest {
		if s.pgid > 1 && !isTerminal(s.status) {
			groups = append(groups, s.pgid)
		}
	}

	return groups
}

// runnerEnv pins provider, model, and base URL. The runner applies workspace
// .env values only to unset variables, so pinning them (even to "") stops a
// workspace .env from overriding them.
func runnerEnv(req domain.RunRequest) []string {
	pinned := map[string]string{
		"UNREAL_HARNESS_LLM_PROVIDER": req.Provider,
		"UNREAL_HARNESS_LLM_MODEL":    req.Model,
		"UNREAL_HARNESS_LLM_BASE_URL": req.BaseURL,
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := pinned[name]; !ok {
			env = append(env, kv)
		}
	}
	for name, value := range pinned {
		env = append(env, name+"="+value)
	}

	return env
}

func dirSize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable entries don't count toward the limit
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}

		return nil
	})

	return size
}
