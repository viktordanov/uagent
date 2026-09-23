// Command uagent wraps unreal-agent-runner: it runs one task with safety
// guards, streams readable progress to stderr, prints the final answer to
// stdout, and records per-run stats for comparison with other harnesses.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

const (
	exitOK        = 0
	exitFailed    = 1
	exitUsage     = 2
	exitDiskLimit = 3
	exitTimeout   = 124
	exitInterrupt = 130
)

const defaultCodexModel = "gpt-6-sol"

var (
	efforts   = []string{"low", "medium", "high", "xhigh", "max"}
	providers = []string{"openai", "openai-codex", "openrouter", "fireworks", "ollama"}
)

func main() {
	err := newApp().Run(context.Background(), os.Args)
	if err == nil {
		return
	}
	code := exitUsage
	var exitErr cli.ExitCoder
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	if msg := err.Error(); msg != "" {
		pal := newPalette(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s %s\n", pal.red("uagent:"), msg)
	}
	os.Exit(code)
}

func newApp() *cli.Command {
	return &cli.Command{
		Name:      "uagent",
		Usage:     "run one unreal-agent-runner task with guards and stats",
		ArgsUsage: "<prompt>",
		Description: "Stdout gets the final answer (or the summary JSON with --json); progress and the\n" +
			"summary go to stderr. The prompt is read from stdin when omitted or \"-\".\n" +
			"Each run is saved under <state-dir>/runs/.\n\n" +
			"Exit codes: 0 ok, 1 failed, 2 usage/preflight, 3 disk limit, 124 timeout, 130 interrupted.",
		// Errors are printed once, by main, with the right exit code.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
		OnUsageError:   onUsageError,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:      "provider",
				Usage:     "LLM provider: " + strings.Join(providers, ", "),
				Value:     "openai-codex",
				Sources:   cli.EnvVars("UNREAL_HARNESS_LLM_PROVIDER"),
				Validator: oneOf("provider", providers),
			},
			&cli.StringFlag{
				Name:        "model",
				Aliases:     []string{"m"},
				Usage:       "model ID",
				DefaultText: defaultCodexModel + " for openai-codex, else provider default",
				Sources:     cli.EnvVars("UNREAL_HARNESS_LLM_MODEL"),
			},
			&cli.StringFlag{
				Name:      "effort",
				Aliases:   []string{"e"},
				Usage:     "thinking level: " + strings.Join(efforts, ", "),
				Value:     "high",
				Validator: oneOf("effort", efforts),
			},
			&cli.DurationFlag{
				Name:    "timeout",
				Aliases: []string{"t"},
				Usage:   "overall wall-clock limit; kills the runner and its tools (0 disables)",
				Value:   30 * time.Minute,
			},
			&cli.StringFlag{
				Name:      "workspace",
				Aliases:   []string{"C"},
				Usage:     "agent workspace and Bash working directory",
				Value:     ".",
				TakesFile: true,
			},
			&cli.StringFlag{
				Name:      "state-dir",
				Usage:     "sessions, logs, and run records; must be outside the workspace",
				Value:     defaultStateDir(),
				Sources:   cli.EnvVars("UAGENT_STATE_DIR"),
				TakesFile: true,
			},
			&cli.StringFlag{
				Name:        "session",
				Usage:       "session ID to create or resume",
				DefaultText: "new UUID",
			},
			&cli.StringFlag{Name: "system-prompt", Usage: "replace the default system prompt"},
			&cli.StringFlag{
				Name:        "base-url",
				Usage:       "LLM base URL override",
				DefaultText: "provider default",
				Sources:     cli.EnvVars("UNREAL_HARNESS_LLM_BASE_URL"),
			},
			&cli.StringFlag{
				Name:        "runner",
				Usage:       "path to unreal-agent-runner",
				DefaultText: "~/.local/bin, then PATH",
				Sources:     cli.EnvVars("UAGENT_RUNNER"),
				TakesFile:   true,
			},
			&cli.StringFlag{
				Name:  "max-disk",
				Usage: "kill the run when tool output exceeds this size, e.g. 500M (0 disables)",
				Value: "5G",
				Validator: func(v string) error {
					_, err := parseSize(v)
					return err
				},
			},
			&cli.IntFlag{Name: "max-attempts", Usage: "LLM retry attempts (0 uses the runner default)"},
			&cli.StringSliceFlag{Name: "disallow", Usage: "tool name to disable, e.g. ViewImage (repeatable)"},
			&cli.BoolFlag{Name: "json", Usage: "print the summary JSON (with answer) to stdout instead of the answer"},
			&cli.BoolFlag{Name: "quiet", Aliases: []string{"q"}, Usage: "no progress output, summary only"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "also show reasoning summaries"},
			&cli.BoolFlag{
				Name:  "allow-dotenv",
				Usage: "run even if the workspace .env sets UNREAL_HARNESS_*, proxy, or Codex auth variables",
			},
		},
		Action: runAction,
		Commands: []*cli.Command{
			{
				Name:         "stats",
				Usage:        "recompute the summary for a saved run",
				ArgsUsage:    "<run-dir|events.jsonl>",
				Flags:        []cli.Flag{&cli.BoolFlag{Name: "json", Usage: "print summary JSON"}},
				OnUsageError: onUsageError,
				Action:       statsAction,
			},
		},
	}
}

func oneOf(flag string, allowed []string) func(string) error {
	return func(v string) error {
		if !slices.Contains(allowed, v) {
			return fmt.Errorf("invalid --%s %q (want %s)", flag, v, strings.Join(allowed, ", "))
		}
		return nil
	}
}

// onUsageError reports bad flags in one line instead of dumping the full help.
func onUsageError(_ context.Context, _ *cli.Command, err error, _ bool) error {
	return usageError("%v (see --help)", err)
}

func usageError(format string, a ...any) error {
	return cli.Exit(fmt.Sprintf(format, a...), exitUsage)
}

type options struct {
	provider, model, effort string
	baseURL                 string
	timeout                 time.Duration
	maxDisk                 int64
	quiet, verbose, jsonOut bool
}

func runAction(_ context.Context, cmd *cli.Command) error {
	o := options{
		provider: cmd.String("provider"),
		model:    cmd.String("model"),
		effort:   cmd.String("effort"),
		baseURL:  cmd.String("base-url"),
		timeout:  cmd.Duration("timeout"),
		quiet:    cmd.Bool("quiet"),
		verbose:  cmd.Bool("verbose"),
		jsonOut:  cmd.Bool("json"),
	}
	o.maxDisk, _ = parseSize(cmd.String("max-disk")) // validated by the flag
	if o.model == "" && o.provider == "openai-codex" {
		o.model = defaultCodexModel
	}
	pal := newPalette(os.Stderr)

	prompt, err := readPrompt(cmd.Args().Slice())
	if err != nil {
		return usageError("%v", err)
	}
	workspace, err := filepath.Abs(cmd.String("workspace"))
	if err != nil {
		return usageError("%v", err)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		return usageError("workspace %s is not a directory", workspace)
	}
	stateDir, err := filepath.Abs(cmd.String("state-dir"))
	if err != nil {
		return usageError("%v", err)
	}
	// Issue #3: sessions inside the workspace can be read back by the agent's
	// own background greps and grow without bound.
	if isWithin(realPath(stateDir), realPath(workspace)) {
		return usageError("state dir %s is inside the workspace; pick one outside it with --state-dir", stateDir)
	}

	runner, err := resolveRunner(cmd.String("runner"))
	if err != nil {
		return usageError("%v", err)
	}
	risky, err := checkDotEnv(workspace)
	if err != nil {
		return usageError("%v", err)
	}
	if len(risky) > 0 {
		msg := fmt.Sprintf("workspace .env sets %s, which can redirect the model endpoint or credentials", strings.Join(risky, ", "))
		if !cmd.Bool("allow-dotenv") {
			return usageError("%s; refusing to run (override with --allow-dotenv)", msg)
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", pal.yellow("uagent: warning:"), msg)
	}
	warning, err := checkAuth(o.provider)
	if err != nil {
		return usageError("%v", err)
	}
	if warning != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", pal.yellow("uagent: warning:"), warning)
	}

	sessionID := cmd.String("session")
	if sessionID == "" {
		sessionID = newUUID()
	}
	started := time.Now()
	runDir := filepath.Join(stateDir, "runs", started.Format("20060102-150405")+"-"+sessionID[:min(8, len(sessionID))])
	sessionsDir := filepath.Join(stateDir, "sessions")
	logsDir := filepath.Join(stateDir, "logs")
	for _, dir := range []string{runDir, sessionsDir, logsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return usageError("%v", err)
		}
	}

	request := map[string]any{
		"prompt":         prompt,
		"thinking_level": o.effort,
		"session_id":     sessionID,
	}
	if o.model != "" {
		request["model"] = o.model
	}
	if v := cmd.String("system-prompt"); v != "" {
		request["system_prompt"] = v
	}
	if v := cmd.StringSlice("disallow"); len(v) > 0 {
		request["disallowed_tools"] = v
	}
	if v := cmd.Int("max-attempts"); v > 0 {
		request["max_attempts"] = v
	}
	requestJSON, _ := json.Marshal(request)
	if err := os.WriteFile(filepath.Join(runDir, "request.json"), requestJSON, 0o600); err != nil {
		return usageError("%v", err)
	}

	if !o.quiet {
		fmt.Fprintf(os.Stderr, "%s %s/%s · effort %s · %s\n%s\n",
			pal.bold("uagent"), o.provider, displayModel(o.model), o.effort, workspace, pal.dim("run "+runDir))
	}
	var progress io.Writer = os.Stderr
	if o.quiet {
		progress = nil
	}
	tracker := NewTracker(progress, o.verbose, pal)
	outcome, err := runProcess(processSpec{
		bin: runner,
		args: []string{
			"-workspace", workspace,
			"-session-directory", sessionsDir,
			"-log-directory", logsDir,
		},
		env:         runnerEnv(o),
		dir:         workspace,
		stdin:       requestJSON,
		eventsPath:  filepath.Join(runDir, "events.jsonl"),
		stderrPath:  filepath.Join(runDir, "stderr.log"),
		sessionFile: filepath.Join(sessionsDir, sessionID+".session.jsonl"),
		opsDir:      filepath.Join(sessionsDir, "operations", sessionID),
		timeout:     o.timeout,
		maxDisk:     o.maxDisk,
		tracker:     tracker,
		notify:      func(msg string) { fmt.Fprintf(os.Stderr, "%s %s\n", pal.yellow("uagent:"), msg) },
	})
	if err != nil {
		return cli.Exit(err.Error(), exitFailed)
	}

	tracker.Close(time.Now())
	summary := Summary{
		Status:         outcome.status,
		ExitCode:       exitCodeFor(outcome.status),
		RunnerExitCode: outcome.runnerExit,
		Provider:       o.provider,
		Model:          displayModel(o.model),
		Effort:         o.effort,
		SessionID:      sessionID,
		Workspace:      workspace,
		RunDir:         runDir,
		StartedAt:      started.Format(time.RFC3339),
		WallSeconds:    time.Since(started).Seconds(),
		Stats:          tracker.Stats(),
		Answer:         tracker.Answer(),
	}
	if encoded, err := json.MarshalIndent(summary, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(runDir, "summary.json"), append(encoded, '\n'), 0o600)
	}
	if summary.Status == "error" && len(summary.Errors) == 0 {
		printStderrTail(filepath.Join(runDir, "stderr.log"), pal)
	}

	fmt.Fprintln(os.Stderr)
	printSummary(os.Stderr, summary, pal)
	if o.jsonOut {
		encoded, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(encoded))
	} else if summary.Answer != "" {
		fmt.Println(summary.Answer)
	}
	if summary.ExitCode != exitOK {
		return cli.Exit("", summary.ExitCode)
	}
	return nil
}

// runnerEnv pins provider, model, and base URL in the child environment. The
// runner only applies .env values for variables that are not already set, so
// pinning them (even to "") stops a workspace .env from overriding them.
func runnerEnv(o options) []string {
	pinned := map[string]string{
		"UNREAL_HARNESS_LLM_PROVIDER": o.provider,
		"UNREAL_HARNESS_LLM_MODEL":    o.model,
		"UNREAL_HARNESS_LLM_BASE_URL": o.baseURL,
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

func exitCodeFor(status string) int {
	switch status {
	case "ok":
		return exitOK
	case "timeout":
		return exitTimeout
	case "interrupted":
		return exitInterrupt
	case "disk_limit":
		return exitDiskLimit
	default:
		return exitFailed
	}
}

func statsAction(_ context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return usageError("usage: uagent stats [--json] <run-dir|events.jsonl>")
	}
	path := cmd.Args().First()
	var summary Summary
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		// Keep run metadata (model, wall time, ...) from the saved summary; recompute stats from events.
		if data, err := os.ReadFile(filepath.Join(path, "summary.json")); err == nil {
			_ = json.Unmarshal(data, &summary)
		}
		path = filepath.Join(path, "events.jsonl")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cli.Exit(err.Error(), exitFailed)
	}
	tracker := NewTracker(nil, false, palette{})
	for line := range strings.Lines(string(data)) {
		if strings.TrimSpace(line) != "" {
			tracker.HandleLine([]byte(line))
		}
	}
	summary.Stats = tracker.Stats()
	summary.Answer = tracker.Answer()
	if summary.Status == "" {
		summary.Status = "ok"
		if tracker.HasError() {
			summary.Status = "error"
		}
		summary.ExitCode = exitCodeFor(summary.Status)
		summary.WallSeconds = summary.EventSeconds
	}
	if cmd.Bool("json") {
		encoded, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(encoded))
		return nil
	}
	printSummary(os.Stdout, summary, newPalette(os.Stdout))
	return nil
}

func readPrompt(args []string) (string, error) {
	prompt := strings.Join(args, " ")
	if prompt == "" || prompt == "-" {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New("no prompt: pass it as an argument or pipe it on stdin (see --help)")
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		prompt = string(data)
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is empty")
	}
	return prompt, nil
}

func printStderrTail(path string, pal palette) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	fmt.Fprintln(os.Stderr, pal.dim("── runner stderr (tail) ──"))
	for _, line := range lines[max(0, len(lines)-20):] {
		fmt.Fprintln(os.Stderr, line)
	}
}

func displayModel(model string) string {
	if model == "" {
		return "(provider default)"
	}
	return model
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
