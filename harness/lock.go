package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// ErrSessionBusy means another run holds the session. The runner does not lock
// its session files, and two processes appending to one would corrupt it.
var ErrSessionBusy = errors.New("session is in use by another run")

// LockSession takes an exclusive advisory lock on <state>/sessions/<id>.lock
// without waiting. It returns ErrSessionBusy when another process or run holds
// it. Call unlock to release it; the lock also ends when the process exits.
func LockSession(stateDir, sessionID string) (unlock func() error, err error) {
	dir := layout{root: stateDir}.sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, sessionID+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open session lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrSessionBusy, sessionID)
		}

		return nil, fmt.Errorf("failed to lock session: %w", err)
	}

	return func() error {
		unlockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if err := f.Close(); err != nil {
			return errors.Join(unlockErr, fmt.Errorf("failed to close session lock: %w", err))
		}
		if unlockErr != nil {
			return fmt.Errorf("failed to unlock session: %w", unlockErr)
		}

		return nil
	}, nil
}
