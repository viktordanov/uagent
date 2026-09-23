package domain_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/viktordanov/uagent/domain"
	"github.com/viktordanov/uagent/testing/fixtures"
	"github.com/viktordanov/uagent/testing/mocks"
)

type recorder struct{ events []domain.Event }

func (r *recorder) Emit(e domain.Event) { r.events = append(r.events, e) }

type testHarness struct {
	preflight *mocks.MockPreflight
	runner    *mocks.MockRunner
	store     *mocks.MockRunStore
	sink      *recorder
	svc       domain.RunService
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	h := &testHarness{
		preflight: mocks.NewMockPreflight(t),
		runner:    mocks.NewMockRunner(t),
		store:     mocks.NewMockRunStore(t),
		sink:      &recorder{},
	}
	h.svc = domain.NewRunService(h.preflight, h.runner, h.store)

	return h
}

func (h *testHarness) expectPreflight(findings ...domain.Finding) {
	h.preflight.EXPECT().Check(mock.Anything, mock.Anything).Return(findings, nil)
}

// expectRun makes the runner emit events and exit with exit.
func (h *testHarness) expectRun(exit domain.RunnerExit, events ...domain.Event) {
	h.runner.EXPECT().Run(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.RunRequest, emit func(domain.Event)) (domain.RunnerExit, error) {
			for _, e := range events {
				emit(e)
			}
			exit.EndedAt = time.Now()

			return exit, nil
		})
}

// expectRunUntilCanceled makes the runner block until its context ends.
func (h *testHarness) expectRunUntilCanceled() {
	h.runner.EXPECT().Run(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ domain.RunRequest, _ func(domain.Event)) (domain.RunnerExit, error) {
			<-ctx.Done()

			return domain.RunnerExit{Code: -1, Termination: domain.TerminationCanceled, EndedAt: time.Now()}, nil
		})
}

func (h *testHarness) expectSave() {
	h.store.EXPECT().Save(mock.Anything, mock.Anything).Return(nil)
}

func TestRunService_Run(t *testing.T) {
	ctx := context.Background()
	exited := func(code int) domain.RunnerExit {
		return domain.RunnerExit{Code: code, Termination: domain.TerminationExited}
	}
	answer := domain.AssistantMessage{At: fixtures.At(time.Second), Text: "done", Final: true}

	t.Run("blocking finding stops the run before the runner starts", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight(domain.Finding{Code: "auth_missing", Severity: domain.SeverityBlocking, Message: "run codex login"})

		_, err := h.svc.Run(ctx, fixtures.Request(), h.sink)

		require.ErrorIs(t, err, domain.ErrPreflightBlocked)
		assert.ErrorContains(t, err, "run codex login")
		assert.Empty(t, h.sink.events)
	})

	t.Run("allow-dotenv downgrades the dotenv finding to a warning", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight(domain.Finding{Code: domain.FindingDotenvRisky, Severity: domain.SeverityBlocking, Message: "risky .env"})
		h.expectRun(exited(0), answer)
		h.expectSave()
		req := fixtures.RequestWith(func(r *domain.RunRequest) { r.AllowDotenv = true })

		result, err := h.svc.Run(ctx, req, h.sink)

		require.NoError(t, err)
		assert.Equal(t, domain.StatusOK, result.Status)
		assert.Equal(t, []string{"risky .env"}, result.Stats.Warnings)
		assert.Contains(t, h.sink.events, domain.Event(domain.PreflightWarning{At: result.StartedAt, Code: domain.FindingDotenvRisky, Message: "risky .env"}))
	})

	t.Run("status from runner exit", func(t *testing.T) {
		tests := map[string]struct {
			exit   domain.RunnerExit
			events []domain.Event
			want   domain.Status
		}{
			"clean exit":          {exit: exited(0), events: []domain.Event{answer}, want: domain.StatusOK},
			"nonzero exit":        {exit: exited(1), want: domain.StatusFailed},
			"runner error event":  {exit: exited(0), events: []domain.Event{domain.RunnerError{Message: "boom"}}, want: domain.StatusFailed},
			"disk limit":          {exit: domain.RunnerExit{Code: -1, Termination: domain.TerminationDiskLimit}, want: domain.StatusDiskLimit},
			"model failure":       {exit: exited(0), events: []domain.Event{domain.ModelResponded{Failure: "rate_limited: slow down"}}, want: domain.StatusFailed},
			"exit without answer": {exit: exited(0), want: domain.StatusOK},
		}
		for name, tc := range tests {
			t.Run(name, func(t *testing.T) {
				h := newTestHarness(t)
				h.expectPreflight()
				h.expectRun(tc.exit, tc.events...)
				h.expectSave()

				result, err := h.svc.Run(ctx, fixtures.Request(), h.sink)

				require.NoError(t, err)
				assert.Equal(t, tc.want, result.Status)
				assert.Equal(t, tc.exit.Code, result.RunnerExitCode)
			})
		}
	})

	t.Run("timeout cancels the runner and reports timeout", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight()
		h.expectRunUntilCanceled()
		h.expectSave()
		req := fixtures.RequestWith(func(r *domain.RunRequest) { r.Timeout = 20 * time.Millisecond })

		result, err := h.svc.Run(ctx, req, h.sink)

		require.NoError(t, err)
		assert.Equal(t, domain.StatusTimeout, result.Status)
	})

	t.Run("parent cancellation reports interrupted", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight()
		h.expectRunUntilCanceled()
		h.expectSave()
		parent, cancel := context.WithCancel(ctx)
		time.AfterFunc(20*time.Millisecond, cancel)

		result, err := h.svc.Run(parent, fixtures.Request(), h.sink)

		require.NoError(t, err)
		assert.Equal(t, domain.StatusInterrupted, result.Status)
	})

	t.Run("emits run_started first and run_finished last", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight()
		h.expectRun(exited(0), answer)
		h.expectSave()
		req := fixtures.RequestWith(func(r *domain.RunRequest) { r.RunID, r.SessionID = "", "" })

		result, err := h.svc.Run(ctx, req, h.sink)

		require.NoError(t, err)
		assert.NotEmpty(t, result.Request.SessionID)
		assert.Contains(t, result.Request.RunID, result.Request.SessionID[:8])
		require.Len(t, h.sink.events, 3)
		assert.IsType(t, domain.RunStarted{}, h.sink.events[0])
		finished, ok := h.sink.events[2].(domain.RunFinished)
		require.True(t, ok)
		assert.Equal(t, "done", finished.Result.Answer)
	})

	t.Run("runner start failure is an error", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight()
		h.runner.EXPECT().Run(mock.Anything, mock.Anything, mock.Anything).Return(domain.RunnerExit{}, errors.New("exec format error"))

		_, err := h.svc.Run(ctx, fixtures.Request(), h.sink)

		assert.ErrorContains(t, err, "exec format error")
	})

	t.Run("store failure returns the result and an error", func(t *testing.T) {
		h := newTestHarness(t)
		h.expectPreflight()
		h.expectRun(exited(0), answer)
		h.store.EXPECT().Save(mock.Anything, mock.Anything).Return(errors.New("disk full"))

		result, err := h.svc.Run(ctx, fixtures.Request(), h.sink)

		require.ErrorContains(t, err, "disk full")
		assert.Equal(t, "done", result.Answer)
		assert.IsType(t, domain.RunFinished{}, h.sink.events[len(h.sink.events)-1], "run_finished is still emitted")
	})
}
