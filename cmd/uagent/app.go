package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/viktordanov/uagent/core"
	"github.com/viktordanov/uagent/harness"
	"github.com/viktordanov/uagent/stream"
)

const (
	codexProvider     = "openai-codex"
	defaultCodexModel = "gpt-6-sol"
)

var (
	efforts   = []string{"low", "medium", "high", "xhigh", "max"}
	providers = []string{"openai", codexProvider, "openrouter", "fireworks", "ollama"}
	logLevels = map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
)

func newApp() *cli.Command {
	return &cli.Command{
		Name:      "uagent",
		Usage:     "run one unreal-agent-runner task with guards and stats",
		Version:   buildVersion(),
		ArgsUsage: "<prompt>",
		Description: "Stdout gets the final answer (the summary JSON with --json, or a JSONL event\n" +
			"stream with --stream); progress and the summary go to stderr. The prompt is read\n" +
			"from stdin when omitted or \"-\". Each run is saved under <state-dir>/runs/.\n\n" +
			"Exit codes: 0 ok, 1 failed, 2 usage/preflight, 3 disk limit, 124 timeout, 130 interrupted.",
		// Errors are printed once, by main, with the right exit code.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   onUsageError,
		Flags:          runFlags(),
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
			{Flags: [][]cli.Flag{{jsonFlag}, {streamFlag}}},
		},
		Action: runAction,
		Commands: []*cli.Command{
			{
				Name:         "stats",
				Usage:        "recompute the summary for a saved run",
				ArgsUsage:    "<run-dir|events.jsonl>",
				Flags:        []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "print the summary JSON"}},
				OnUsageError: onUsageError,
				Action:       statsAction,
			},
		},
	}
}

var (
	jsonFlag   = &cli.BoolFlag{Name: "json", Usage: "print the summary JSON (with answer) to stdout instead of the answer"}
	streamFlag = &cli.BoolFlag{Name: "stream", Usage: "write normalized JSONL events (schema v" + strconv.Itoa(stream.SchemaVersion) + ") to stdout; no other output"}
)

func runFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "provider", Usage: "LLM provider: " + strings.Join(providers, ", "), Value: codexProvider,
			Sources: cli.EnvVars("UNREAL_HARNESS_LLM_PROVIDER"), Validator: oneOf("provider", providers),
		},
		&cli.StringFlag{
			Name: "model", Aliases: []string{"m"}, Usage: "model ID",
			DefaultText: defaultCodexModel + " for openai-codex, else provider default",
			Sources:     cli.EnvVars("UNREAL_HARNESS_LLM_MODEL"),
		},
		&cli.StringFlag{
			Name: "effort", Aliases: []string{"e"}, Usage: "thinking level: " + strings.Join(efforts, ", "),
			Value: "high", Validator: oneOf("effort", efforts),
		},
		&cli.DurationFlag{
			Name: "timeout", Aliases: []string{"t"}, Value: 30 * time.Minute,
			Usage: "overall wall-clock limit; kills the runner and its tools (0 disables)",
		},
		&cli.StringFlag{Name: "workspace", Aliases: []string{"C"}, Usage: "agent workspace and Bash working directory", Value: ".", TakesFile: true},
		&cli.StringFlag{
			Name: "state-dir", Usage: "sessions, logs, and run records; must be outside the workspace",
			Value: harness.DefaultStateDir(), Sources: cli.EnvVars("UAGENT_STATE_DIR"), TakesFile: true,
		},
		&cli.StringFlag{Name: "session", Usage: "session ID to create or resume", DefaultText: "new UUID"},
		&cli.StringFlag{Name: "system-prompt", Usage: "replace the default system prompt"},
		&cli.StringFlag{
			Name: "base-url", Usage: "LLM base URL override", DefaultText: "provider default",
			Sources: cli.EnvVars("UNREAL_HARNESS_LLM_BASE_URL"),
		},
		&cli.StringFlag{
			Name: "runner", Usage: "path to unreal-agent-runner", DefaultText: "~/.local/bin, then PATH",
			Sources: cli.EnvVars("UAGENT_RUNNER"), TakesFile: true,
		},
		&cli.StringFlag{
			Name: "max-disk", Usage: "kill the run when tool output exceeds this size, e.g. 500M (0 disables)", Value: "5G",
			Validator: func(v string) error {
				_, err := parseSize(v)

				return err
			},
		},
		&cli.IntFlag{Name: "max-attempts", Usage: "LLM retry attempts (0 uses the runner default)"},
		&cli.StringSliceFlag{Name: "disallow", Usage: "tool name to disable, e.g. ViewImage (repeatable)"},
		jsonFlag,
		streamFlag,
		&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Usage: "no progress output, summary only"},
		&cli.BoolFlag{Name: "verbose", Usage: "also show reasoning summaries"},
		&cli.BoolFlag{Name: "allow-dotenv", Usage: "run even if the workspace .env sets UNREAL_HARNESS_*, proxy, or Codex auth variables"},
		&cli.StringFlag{Name: "log-level", Usage: "diagnostic log level on stderr: debug, info, warn, error", Value: "warn", Validator: oneOfMap("log-level", logLevels)},
	}
}

func runAction(ctx context.Context, cmd *cli.Command) error {
	prompt, err := readPrompt(cmd.Args().Slice())
	if err != nil {
		return err
	}
	workspace, err := filepath.Abs(cmd.String("workspace"))
	if err != nil {
		return fmt.Errorf("failed to resolve workspace: %w", err)
	}
	stateDir, err := filepath.Abs(cmd.String("state-dir"))
	if err != nil {
		return fmt.Errorf("failed to resolve state dir: %w", err)
	}
	bin, err := harness.FindRunner(cmd.String("runner"))
	if err != nil {
		return usageError("%v", err)
	}
	maxDisk, _ := parseSize(cmd.String("max-disk")) // validated by the flag
	provider, model := cmd.String("provider"), cmd.String("model")
	if model == "" && provider == codexProvider {
		model = defaultCodexModel
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevels[cmd.String("log-level")]}))
	h := harness.New(harness.Config{RunnerPath: bin, StateDir: stateDir, MaxDisk: maxDisk, Logger: logger})

	req := core.Request{
		SessionID:       cmd.String("session"),
		Prompt:          prompt,
		Provider:        provider,
		Model:           model,
		Effort:          cmd.String("effort"),
		BaseURL:         cmd.String("base-url"),
		SystemPrompt:    cmd.String("system-prompt"),
		DisallowedTools: cmd.StringSlice("disallow"),
		MaxAttempts:     cmd.Int("max-attempts"),
		Workspace:       workspace,
		Timeout:         cmd.Duration("timeout"),
		AllowDotenv:     cmd.Bool("allow-dotenv"),
	}

	pal := PaletteFor(os.Stderr)
	streaming := cmd.Bool("stream")
	sink := core.Sink(func(core.Event) {})
	var streamSink *stream.Sink
	switch {
	case streaming:
		streamSink = stream.NewSink(os.Stdout)
		sink = streamSink.Emit
	case !cmd.Bool("quiet"):
		sink = NewProgress(os.Stderr, pal, cmd.Bool("verbose")).Emit
	}

	result, err := h.Run(ctx, req, sink)
	if err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}
	logRun(ctx, logger, result)
	if streamSink != nil {
		if err := streamSink.Err(); err != nil {
			return err
		}
	}
	runDir := h.RunDir(result.Request.RunID)
	if !streaming {
		if result.Status == core.StatusFailed && len(result.Stats.Errors) == 0 {
			printStderrTail(os.Stderr, filepath.Join(runDir, harness.StderrFile), pal)
		}
		fmt.Fprintln(os.Stderr)
		Summary(os.Stderr, result, runDir, pal)
		if err := printResult(os.Stdout, result, cmd.Bool("json")); err != nil {
			return err
		}
	}
	if code := statusExitCode(result.Status); code != exitOK {
		return cli.Exit("", code)
	}

	return nil
}

func statsAction(_ context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return usageError("usage: uagent stats [--json] <run-dir|events.jsonl>")
	}
	path := cmd.Args().First()
	result, err := harness.Load(path)
	if err != nil {
		return fmt.Errorf("failed to load run: %w", err)
	}
	if cmd.Bool("json") {
		return printResult(os.Stdout, result, true)
	}
	runDir := ""
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		runDir = path
	}
	Summary(os.Stdout, result, runDir, PaletteFor(os.Stdout))

	return nil
}

// logRun emits one wide event per run for diagnostics (visible at --log-level info).
func logRun(ctx context.Context, logger *slog.Logger, r core.Result) {
	logger.LogAttrs(ctx, slog.LevelInfo, "run finished",
		slog.String("run_id", r.Request.RunID),
		slog.String("session_id", r.Request.SessionID),
		slog.String("provider", r.Request.Provider),
		slog.String("model", r.Request.Model),
		slog.String("status", string(r.Status)),
		slog.Int("runner_exit_code", r.RunnerExitCode),
		slog.Int64("duration_ms", r.Wall.Milliseconds()),
		slog.Int("turns", r.Stats.Turns),
		slog.Int("tool_calls", r.Stats.ToolCalls),
		slog.Int64("input_tokens", r.Stats.Tokens.InputTokens),
		slog.Int64("output_tokens", r.Stats.Tokens.OutputTokens),
	)
}

func printResult(w io.Writer, r core.Result, asJSON bool) error {
	if !asJSON {
		if r.Answer != "" {
			fmt.Fprintln(w, r.Answer)
		}

		return nil
	}
	encoded, err := json.MarshalIndent(stream.SummaryToDTO(r), "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode summary: %w", err)
	}
	fmt.Fprintln(w, string(encoded))

	return nil
}

func onUsageError(_ context.Context, _ *cli.Command, err error, _ bool) error {
	return usageError("%v (see --help)", err)
}

func usageError(format string, a ...any) error {
	return cli.Exit(fmt.Sprintf(format, a...), exitUsage)
}

func oneOf(flag string, allowed []string) func(string) error {
	return func(v string) error {
		if !slices.Contains(allowed, v) {
			return fmt.Errorf("invalid --%s %q (want %s)", flag, v, strings.Join(allowed, ", "))
		}

		return nil
	}
}

func oneOfMap[V any](flag string, allowed map[string]V) func(string) error {
	return func(v string) error {
		if _, ok := allowed[v]; !ok {
			return fmt.Errorf("invalid --%s %q", flag, v)
		}

		return nil
	}
}

func readPrompt(args []string) (string, error) {
	prompt := strings.Join(args, " ")
	if prompt == "" || prompt == "-" {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", usageError("no prompt; pass it as an argument or pipe it on stdin (see --help)")
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read prompt from stdin: %w", err)
		}
		prompt = string(data)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", usageError("prompt is empty")
	}

	return prompt, nil
}

func printStderrTail(w io.Writer, path string, pal Palette) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	fmt.Fprintln(w, pal.Dim("── runner stderr (tail) ──"))
	for _, line := range lines[max(0, len(lines)-20):] {
		fmt.Fprintln(w, line)
	}
}

// parseSize parses sizes like 500M, 5G, or 1024 (bytes). "0" disables the limit.
func parseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(s), "B"))
	mult := int64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K':
			mult, s = 1<<10, s[:n-1]
		case 'M':
			mult, s = 1<<20, s[:n-1]
		case 'G':
			mult, s = 1<<30, s[:n-1]
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}

	return int64(v * float64(mult)), nil
}
