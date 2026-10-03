package procstart

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func read(pid int) (boot, start string, err error) {
	boot, err = unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", "", fmt.Errorf("failed to read the boot session: %w", err)
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return "", "", fmt.Errorf("failed to read process %d: %w", pid, err)
	}
	if len(procs) == 0 {
		return "", "", fmt.Errorf("process %d: %w", pid, ErrNoProcess)
	}
	t := procs[0].Proc.P_starttime

	return boot, darwinStartTime(t.Sec, t.Usec), nil
}
