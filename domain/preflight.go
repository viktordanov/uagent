package domain

import "context"

// Severity decides whether a preflight finding stops the run.
type Severity string

const (
	SeverityBlocking Severity = "blocking"
	SeverityWarning  Severity = "warning"
)

// FindingDotenvRisky marks a workspace .env that can redirect the model
// endpoint or credentials. RunRequest.AllowDotenv downgrades it to a warning.
const FindingDotenvRisky = "dotenv_risky"

// Finding is one preflight problem. Findings are results, not errors.
type Finding struct {
	Code     string
	Severity Severity
	Message  string
}

// Preflight inspects the environment before a run starts.
type Preflight interface {
	Check(ctx context.Context, req RunRequest) ([]Finding, error)
}
