package harness

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/viktordanov/uagent/core"
)

// RunnerBackend spawns unreal-agent-runner in its own process group.
type RunnerBackend struct {
	// Path is the runner executable; see FindRunner.
	Path string
	// Env adds NAME=value variables to the runner's environment, after the
	// inherited ones, so they win. The pinned provider variables still win
	// over them.
	Env []string
}

// Start implements Backend.
func (b RunnerBackend) Start(_ context.Context, l Launch) (Process, error) {
	cmd := exec.Command(b.Path, //nolint:noctx // the harness handles cancellation, including tool process groups
		"-workspace", l.Request.Workspace,
		"-session-directory", l.SessionsDir,
		"-log-directory", l.LogsDir,
	)
	cmd.Dir = l.Request.Workspace
	cmd.Env = runnerEnv(l.Request, b.Env)
	cmd.Stdin = bytes.NewReader(l.RunnerRequest)
	// Stdout is the harness's pipe; exec hands its descriptor to the runner.
	cmd.Stdout, cmd.Stderr = l.Stdout, l.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	err := cmd.Start()
	_ = l.Stdout.Close() // the runner holds its own copy
	if err != nil {
		return nil, fmt.Errorf("failed to start runner: %w", err)
	}
	p := &runnerProcess{cmd: cmd, pgid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait() // the exit code is read from ProcessState
		close(p.done)
	}()

	return p, nil
}

type runnerProcess struct {
	cmd  *exec.Cmd
	pgid int
	done chan struct{}
}

func (p *runnerProcess) Done() <-chan struct{} { return p.done }
func (p *runnerProcess) ExitCode() int         { return p.cmd.ProcessState.ExitCode() }
func (p *runnerProcess) Interrupt()            { _ = syscall.Kill(p.pgid, syscall.SIGINT) } // ESRCH means it already exited
func (p *runnerProcess) Terminate()            { signalGroups([]int{p.pgid}, syscall.SIGTERM) }
func (p *runnerProcess) Kill()                 { signalGroups([]int{p.pgid}, syscall.SIGKILL) }

// runnerEnv pins provider, model, and base URL. The runner applies workspace
// .env values only to unset variables, so pinning them (even to "") stops a
// workspace .env from overriding them.
func runnerEnv(req core.Request, extra []string) []string {
	pinned := map[string]string{
		"UNREAL_HARNESS_LLM_PROVIDER": req.Provider,
		"UNREAL_HARNESS_LLM_MODEL":    req.Model,
		"UNREAL_HARNESS_LLM_BASE_URL": req.BaseURL,
	}
	var env []string
	for _, kv := range append(os.Environ(), extra...) {
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
