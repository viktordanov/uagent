package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const (
	killGrace     = 5 * time.Second
	drainTimeout  = 2 * time.Second
	diskPollEvery = 5 * time.Second
)

type processSpec struct {
	bin        string
	args       []string
	env        []string
	dir        string
	stdin      []byte
	eventsPath string
	stderrPath string
	// sessionFile holds "operation" records with the process groups of background tools.
	sessionFile string
	// opsDir holds tool output files; watched against maxDisk (issue #3).
	opsDir  string
	timeout time.Duration
	maxDisk int64
	tracker *Tracker
	notify  func(string)
}

type processOutcome struct {
	status     string // ok, error, timeout, interrupted, disk_limit
	runnerExit int
}

func runProcess(spec processSpec) (processOutcome, error) {
	events, err := os.Create(spec.eventsPath)
	if err != nil {
		return processOutcome{}, err
	}
	defer events.Close()
	stderr, err := os.Create(spec.stderrPath)
	if err != nil {
		return processOutcome{}, err
	}
	defer stderr.Close()

	// A raw pipe rather than StdoutPipe: Wait must not block on grandchildren
	// that inherited stdout, and we need to stop reading on our own terms.
	r, w, err := os.Pipe()
	if err != nil {
		return processOutcome{}, err
	}
	cmd := exec.Command(spec.bin, spec.args...)
	cmd.Dir, cmd.Env = spec.dir, spec.env
	cmd.Stdin = bytes.NewReader(spec.stdin)
	cmd.Stdout, cmd.Stderr = w, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		w.Close()
		r.Close()
		return processOutcome{}, fmt.Errorf("start runner: %w", err)
	}
	w.Close()
	pgid := cmd.Process.Pid

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		br := bufio.NewReaderSize(r, 1<<20)
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				events.Write(line)
				spec.tracker.HandleLine(line)
			}
			if err != nil {
				return
			}
		}
	}()
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	var deadline <-chan time.Time
	if spec.timeout > 0 {
		timer := time.NewTimer(spec.timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	disk := time.NewTicker(diskPollEvery)
	defer disk.Stop()

	status := ""
	stop := func(reason, why string) {
		status = reason
		spec.notify(why + "; stopping runner and its background tools")
		teardown(pgid, spec.sessionFile, waitCh)
	}
wait:
	for {
		select {
		case <-waitCh:
			break wait
		case <-deadline:
			stop("timeout", fmt.Sprintf("timeout after %s", spec.timeout))
			break wait
		case <-sigCh:
			stop("interrupted", "interrupted")
			break wait
		case <-disk.C:
			if spec.maxDisk <= 0 {
				continue
			}
			if used := dirSize(spec.opsDir); used > spec.maxDisk {
				stop("disk_limit", fmt.Sprintf("tool output reached %s (limit %s)", humanBytes(used), humanBytes(spec.maxDisk)))
				break wait
			}
		}
	}

	select {
	case <-readDone:
	case <-time.After(drainTimeout):
		r.Close()
		<-readDone
	}
	r.Close()

	exit := cmd.ProcessState.ExitCode()
	if status == "" {
		status = "ok"
		if exit != 0 || spec.tracker.HasError() {
			status = "error"
		}
	}
	return processOutcome{status: status, runnerExit: exit}, nil
}

// teardown stops the runner's process group plus every still-running
// background tool group: SIGTERM, a grace period, then SIGKILL.
func teardown(pgid int, sessionFile string, waitCh <-chan error) {
	groups := append([]int{pgid}, liveOperationGroups(sessionFile)...)
	signalGroups(groups, syscall.SIGTERM)
	select {
	case <-waitCh:
	case <-time.After(killGrace):
		signalGroups(groups, syscall.SIGKILL)
		<-waitCh
	}
	// Tool groups may outlive the runner; make sure they are gone.
	signalGroups(append(groups, liveOperationGroups(sessionFile)...), syscall.SIGKILL)
}

func signalGroups(groups []int, sig syscall.Signal) {
	for _, g := range groups {
		if g > 1 {
			_ = syscall.Kill(-g, sig)
		}
	}
}

// liveOperationGroups reads the session file and returns process groups of
// operations whose latest recorded status is not terminal.
func liveOperationGroups(sessionFile string) []int {
	f, err := os.Open(sessionFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	latest := map[string]struct {
		status string
		pgid   int
	}{}
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		var rec struct {
			Type string `json:"type"`
			Data struct {
				Operation struct {
					ID, Status string
					State      struct{ ProcessGroupID int }
				}
			} `json:"data"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Type == "operation" {
			op := rec.Data.Operation
			entry := latest[op.ID]
			entry.status = op.Status
			if op.State.ProcessGroupID != 0 {
				entry.pgid = op.State.ProcessGroupID
			}
			latest[op.ID] = entry
		}
		if err != nil {
			break
		}
	}
	var groups []int
	for _, e := range latest {
		if e.pgid > 1 && !isTerminal(e.status) {
			groups = append(groups, e.pgid)
		}
	}
	return groups
}

func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
