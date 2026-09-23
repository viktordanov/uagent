// Command uagent runs one unreal-agent-runner task with safety guards, shows
// progress, prints the final answer, and records per-run stats.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
)

const (
	exitOK        = 0
	exitFailed    = 1
	exitUsage     = 2
	exitDiskLimit = 3
	exitTimeout   = 124
	exitInterrupt = 130
)

// version is set with -ldflags "-X main.version=..."; go install builds use the module version.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newApp().Run(ctx, os.Args)
	stop()
	os.Exit(exitCode(err))
}

// exitCode maps the app error to a process exit code and prints it once.
func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	code := exitFailed
	var exitErr cli.ExitCoder
	switch {
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	case errors.Is(err, harness.ErrPreflightBlocked), errors.Is(err, harness.ErrSessionBusy):
		code = exitUsage
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", PaletteFor(os.Stderr).Red("uagent:"), msg)
	}

	return code
}

func statusExitCode(status core.Status) int {
	switch status {
	case core.StatusOK:
		return exitOK
	case core.StatusTimeout:
		return exitTimeout
	case core.StatusInterrupted:
		return exitInterrupt
	case core.StatusDiskLimit:
		return exitDiskLimit
	case core.StatusFailed:
	}

	return exitFailed
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}

	return version
}
