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

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/render"
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
	case errors.Is(err, domain.ErrPreflightBlocked):
		code = exitUsage
	}
	if msg := err.Error(); msg != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", render.PaletteFor(os.Stderr).Red("uagent:"), msg)
	}

	return code
}

func statusExitCode(status domain.Status) int {
	switch status {
	case domain.StatusOK:
		return exitOK
	case domain.StatusTimeout:
		return exitTimeout
	case domain.StatusInterrupted:
		return exitInterrupt
	case domain.StatusDiskLimit:
		return exitDiskLimit
	case domain.StatusFailed:
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
