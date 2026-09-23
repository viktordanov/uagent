// Command uagent wraps unreal-agent-runner: it runs one task with safety
// guards, streams readable progress to stderr, prints the final answer to
// stdout, and records per-run stats for comparison with other harnesses.
package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

const usageHeader = `uagent - run one unreal-agent-runner task with guards and stats

Usage:
  uagent [flags] "<prompt>"        prompt from stdin when omitted or "-"
  uagent stats [-json] <run-dir|events.jsonl>

Stdout gets the final answer (or the summary JSON with -json); progress and
the summary go to stderr. Each run is saved under <state-dir>/runs/.

Exit codes: 0 ok, 1 failed, 2 usage/preflight, 3 disk limit, 124 timeout, 130 interrupted.

Flags:
`

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

type options struct {
	model, provider, effort string
	timeout                 time.Duration
	workspace, stateDir     string
	session, systemPrompt   string
	baseURL, runner         string
	maxDisk                 string
	maxAttempts             int
	disallow                stringList
	jsonOut, quiet, verbose bool
	allowDotenv             bool
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stats" {
		os.Exit(statsMain(os.Args[2:]))
	}
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	var o options
	fs := flag.NewFlagSet("uagent", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), usageHeader); fs.PrintDefaults() }
	fs.StringVar(&o.provider, "provider", envOr("UNREAL_HARNESS_LLM_PROVIDER", "openai-codex"), "LLM provider: openai, openai-codex, openrouter, fireworks, ollama")
	fs.StringVar(&o.model, "model", os.Getenv("UNREAL_HARNESS_LLM_MODEL"), "model ID (openai-codex default: "+defaultCodexModel+")")
	fs.StringVar(&o.effort, "effort", "high", "thinking level: "+strings.Join(efforts, ", "))
	fs.DurationVar(&o.timeout, "timeout", 30*time.Minute, "overall wall-clock limit; kills the runner and its tools (0 disables)")
	fs.StringVar(&o.workspace, "workspace", ".", "agent workspace and Bash working directory")
	fs.StringVar(&o.workspace, "C", ".", "shorthand for -workspace")
	fs.StringVar(&o.stateDir, "state-dir", defaultStateDir(), "sessions, logs, and run records; must be outside the workspace")
	fs.StringVar(&o.session, "session", "", "session ID to create or resume (default: new UUID)")
	fs.StringVar(&o.systemPrompt, "system-prompt", "", "replace the default system prompt")
	fs.StringVar(&o.baseURL, "base-url", "", "LLM base URL override (default: provider default)")
	fs.StringVar(&o.runner, "runner", "", "path to unreal-agent-runner (default: $UAGENT_RUNNER, ~/.local/bin, PATH)")
	fs.StringVar(&o.maxDisk, "max-disk", "5G", "kill the run when tool output exceeds this size (0 disables)")
	fs.IntVar(&o.maxAttempts, "max-attempts", 0, "LLM retry attempts (default: runner default)")
	fs.Var(&o.disallow, "disallow", "tool name to disable, e.g. ViewImage (repeatable)")
	fs.BoolVar(&o.jsonOut, "json", false, "print the summary JSON (with answer) to stdout instead of the answer")
	fs.BoolVar(&o.quiet, "q", false, "no progress output, summary only")
	fs.BoolVar(&o.verbose, "v", false, "also show reasoning summaries")
	fs.BoolVar(&o.allowDotenv, "allow-dotenv", false, "run even if the workspace .env sets UNREAL_HARNESS_*, proxy, or Codex auth variables")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	pal := newPalette(os.Stderr)
	fail := func(format string, a ...any) int {
		fmt.Fprintf(os.Stderr, "%s %s\n", pal.red("uagent:"), fmt.Sprintf(format, a...))
		return exitUsage
	}

	prompt, err := readPrompt(positional)
	if err != nil {
		return fail("%v", err)
	}
	if !slices.Contains(efforts, o.effort) {
		return fail("invalid -effort %q (want %s)", o.effort, strings.Join(efforts, ", "))
	}
	if o.model == "" && o.provider == "openai-codex" {
		o.model = defaultCodexModel
	}
	maxDisk, err := parseSize(o.maxDisk)
	if err != nil {
		return fail("-max-disk: %v", err)
	}

	workspace, err := filepath.Abs(o.workspace)
	if err != nil {
		return fail("%v", err)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		return fail("workspace %s is not a directory", workspace)
	}
	stateDir, err := filepath.Abs(o.stateDir)
	if err != nil {
		return fail("%v", err)
	}
	// Issue #3: sessions inside the workspace can be read back by the agent's
	// own background greps and grow without bound.
	if isWithin(realPath(stateDir), realPath(workspace)) {
		return fail("state dir %s is inside the workspace; pick one outside it with -state-dir", stateDir)
	}

	runner, err := resolveRunner(o.runner)
	if err != nil {
		return fail("%v", err)
	}
	risky, err := checkDotEnv(workspace)
	if err != nil {
		return fail("%v", err)
	}
	if len(risky) > 0 {
		msg := fmt.Sprintf("workspace .env sets %s, which can redirect the model endpoint or credentials", strings.Join(risky, ", "))
		if !o.allowDotenv {
			return fail("%s; refusing to run (override with -allow-dotenv)", msg)
		}
		fmt.Fprintf(os.Stderr, "%s %s\n", pal.yellow("uagent: warning:"), msg)
	}
	warning, err := checkAuth(o.provider)
	if err != nil {
		return fail("%v", err)
	}
	if warning != "" {
		fmt.Fprintf(os.Stderr, "%s %s\n", pal.yellow("uagent: warning:"), warning)
	}

	sessionID := o.session
	if sessionID == "" {
		sessionID = newUUID()
	}
	started := time.Now()
	runDir := filepath.Join(stateDir, "runs", started.Format("20060102-150405")+"-"+sessionID[:min(8, len(sessionID))])
	sessionsDir := filepath.Join(stateDir, "sessions")
	logsDir := filepath.Join(stateDir, "logs")
	for _, dir := range []string{runDir, sessionsDir, logsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail("%v", err)
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
	if o.systemPrompt != "" {
		request["system_prompt"] = o.systemPrompt
	}
	if len(o.disallow) > 0 {
		request["disallowed_tools"] = []string(o.disallow)
	}
	if o.maxAttempts > 0 {
		request["max_attempts"] = o.maxAttempts
	}
	requestJSON, _ := json.Marshal(request)
	if err := os.WriteFile(filepath.Join(runDir, "request.json"), requestJSON, 0o600); err != nil {
		return fail("%v", err)
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
		maxDisk:     maxDisk,
		tracker:     tracker,
		notify:      func(msg string) { fmt.Fprintf(os.Stderr, "%s %s\n", pal.yellow("uagent:"), msg) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", pal.red("uagent:"), err)
		return exitFailed
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
	return summary.ExitCode
}

// runnerEnv pins provider, model, and base URL in the child environment. The
// runner only applies .env values for variables that are not already set, so
// pinning them (even to "") stops a workspace .env from overriding them.
func runnerEnv(o options) []string {
	baseURL := o.baseURL
	if baseURL == "" {
		baseURL = os.Getenv("UNREAL_HARNESS_LLM_BASE_URL")
	}
	pinned := map[string]string{
		"UNREAL_HARNESS_LLM_PROVIDER": o.provider,
		"UNREAL_HARNESS_LLM_MODEL":    o.model,
		"UNREAL_HARNESS_LLM_BASE_URL": baseURL,
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

func statsMain(args []string) int {
	fs := flag.NewFlagSet("uagent stats", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print summary JSON")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: uagent stats [-json] <run-dir|events.jsonl>")
		return exitUsage
	}
	path := positional[0]
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
		fmt.Fprintf(os.Stderr, "uagent: %v\n", err)
		return exitFailed
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
	if *jsonOut {
		encoded, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(encoded))
		return exitOK
	}
	printSummary(os.Stdout, summary, newPalette(os.Stdout))
	return exitOK
}

// parseInterspersed allows flags before and after positional arguments.
// Everything after a literal "--" is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional, args = append(positional, args[0]), args[1:]
	}
	return append(positional, tail...), nil
}

func readPrompt(positional []string) (string, error) {
	prompt := strings.Join(positional, " ")
	if prompt == "" || prompt == "-" {
		if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
			return "", errors.New("no prompt: pass it as an argument or pipe it on stdin (see -h)")
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

func envOr(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
