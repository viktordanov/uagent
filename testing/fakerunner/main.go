// Command fakerunner stands in for unreal-agent-runner. It accepts the same
// flags and stdin request and replays a captured runner output, so uagent and
// its TUI can be tested and demonstrated without a model or tokens.
//
// Environment:
//
//	FAKERUNNER_FIXTURE  runner stdout to replay (JSONL); required
//	FAKERUNNER_SPEED    replay speed: 0 (default) writes at once, 1 keeps the original timing, 10 is ten times faster
//	FAKERUNNER_EXIT     exit code after the replay (default 0)
//	FAKERUNNER_HANG     "1" starts background tools and waits to be killed; "orphan" starts them and exits
//	FAKERUNNER_CAPTURE  directory that receives stdin.json, env.txt, and the pids of hung tools
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	sessionDir := flag.String("session-directory", ".harness/sessions", "")
	flag.String("log-directory", "", "")
	flag.String("workspace", ".", "")
	flag.Parse()
	code, err := run(*sessionDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakerunner:", err)
		code = 1
	}
	os.Exit(code)
}

func run(sessionDir string) (int, error) {
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 0, fmt.Errorf("read request: %w", err)
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(stdin, &req); err != nil {
		return 0, fmt.Errorf("decode request: %w", err)
	}
	capture := os.Getenv("FAKERUNNER_CAPTURE")
	if capture != "" {
		if err := os.WriteFile(filepath.Join(capture, "stdin.json"), stdin, 0o600); err != nil {
			return 0, err
		}
		env := strings.Join(os.Environ(), "\n") + "\n"
		if err := os.WriteFile(filepath.Join(capture, "env.txt"), []byte(env), 0o600); err != nil {
			return 0, err
		}
	}

	if err := replay(os.Getenv("FAKERUNNER_FIXTURE"), os.Getenv("FAKERUNNER_SPEED")); err != nil {
		return 0, err
	}
	if mode := os.Getenv("FAKERUNNER_HANG"); mode != "" {
		if err := startTools(sessionDir, req.SessionID, capture); err != nil {
			return 0, err
		}
		if mode == "1" {
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
			<-stop

			return 130, nil
		}
	}
	code, _ := strconv.Atoi(os.Getenv("FAKERUNNER_EXIT"))

	return code, nil
}

func replay(path, speedText string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open fixture: %w", err)
	}
	defer f.Close()
	speed, _ := strconv.ParseFloat(speedText, 64)
	var last time.Time
	br := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var item struct {
				RecordedAt time.Time `json:"RecordedAt"`
			}
			if speed > 0 && json.Unmarshal(line, &item) == nil && !item.RecordedAt.IsZero() {
				if !last.IsZero() {
					time.Sleep(time.Duration(float64(item.RecordedAt.Sub(last)) / speed))
				}
				last = item.RecordedAt
			}
			if _, werr := os.Stdout.Write(line); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// startTools behaves like a runner with background tools: one child in the
// runner's process group and one tool in its own group, recorded in the session
// file the way the real runner records operations.
func startTools(sessionDir, sessionID, capture string) error {
	groupChild := exec.CommandContext(context.Background(), "sleep", "300")
	if err := groupChild.Start(); err != nil {
		return err
	}
	tool := exec.CommandContext(context.Background(), "sleep", "300")
	tool.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := tool.Start(); err != nil {
		return err
	}
	record := fmt.Sprintf(`{"type":"operation","data":{"Operation":{"ID":"op-hang","Status":"awaiting","State":{"ProcessGroupID":%d}}}}`+"\n", tool.Process.Pid)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(sessionDir, sessionID+".session.jsonl"), []byte(record), 0o600); err != nil {
		return err
	}
	if capture != "" {
		pids := fmt.Sprintf("%d\n%d\n", groupChild.Process.Pid, tool.Process.Pid)
		if err := os.WriteFile(filepath.Join(capture, "hung.pids"), []byte(pids), 0o600); err != nil {
			return err
		}
	}

	return nil
}
