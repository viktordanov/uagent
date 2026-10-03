package procstart

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

func read(pid int) (boot, start string, err error) {
	id, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", "", fmt.Errorf("failed to read the boot ID: %w", err)
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", fmt.Errorf("process %d: %w", pid, ErrNoProcess)
	}
	if err != nil {
		return "", "", fmt.Errorf("failed to read process %d: %w", pid, err)
	}
	start, err = linuxStartTime(string(stat))
	if err != nil {
		return "", "", fmt.Errorf("process %d: %w", pid, err)
	}

	return strings.TrimSpace(string(id)), start, nil
}
