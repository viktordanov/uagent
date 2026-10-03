// Package procstart identifies a process by the boot it runs in and the time
// it started, so that a recorded process ID can be told apart from a later
// process that reuses it.
//
// uah-core's runner records Of for every tool process group it starts
// (ShellState.ProcessGroupStart) with its own implementation of this format;
// the two must produce the same strings.
package procstart

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoProcess means no process has the process ID.
var ErrNoProcess = errors.New("no such process")

// Of returns the identity of the process pid: "<boot ID>/<start time>". The
// process keeps it while it lives, exec included. A process that reuses the
// ID later, in the same boot or after a reboot, has a different identity.
// It returns ErrNoProcess if the process is gone, and errors.ErrUnsupported
// on a system other than Darwin and Linux.
func Of(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("process ID %d: %w", pid, ErrNoProcess)
	}
	boot, start, err := read(pid)
	if err != nil {
		return "", err
	}

	return boot + "/" + start, nil
}

// linuxStartTime returns field 22 of a /proc/<pid>/stat line: the start time
// in clock ticks after boot. The command name (field 2) is in parentheses and
// can hold spaces and parentheses, so the fields are counted from its last ')'.
func linuxStartTime(stat string) (string, error) {
	_, rest, ok := strings.CutLast(stat, ")")
	if !ok {
		return "", errors.New("process stat has no command name")
	}
	fields := strings.Fields(rest)
	const startTime = 22 - 3 // the fields after ')' start at field 3
	if len(fields) <= startTime {
		return "", errors.New("process stat has no start time")
	}

	return fields[startTime], nil
}

// darwinStartTime formats a Darwin process start time (kp_proc.p_starttime).
func darwinStartTime(sec int64, usec int32) string {
	return fmt.Sprintf("%d.%06d", sec, usec)
}
