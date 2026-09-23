package core

import "strings"

// Severity decides whether a preflight finding stops the run.
type Severity string

const (
	SeverityBlocking Severity = "blocking"
	SeverityWarning  Severity = "warning"
)

// Preflight finding codes.
const (
	FindingWorkspaceMissing = "workspace_missing"
	FindingStateInWorkspace = "state_in_workspace"
	// FindingDotenvRisky marks a workspace .env that can redirect the model
	// endpoint or credentials. Request.AllowDotenv downgrades it to a warning.
	FindingDotenvRisky  = "dotenv_risky"
	FindingAuthMissing  = "auth_missing"
	FindingAuthExpired  = "auth_expired"
	FindingAuthExpiring = "auth_expiring"
)

// Finding is one preflight problem. Findings are results, not errors.
type Finding struct {
	Code     string
	Severity Severity
	Message  string
}

// Triage splits findings into those that block the run and those that only warn.
func Triage(findings []Finding, allowDotenv bool) (blocking, warnings []Finding) {
	for _, f := range findings {
		allowed := f.Code == FindingDotenvRisky && allowDotenv
		if f.Severity == SeverityBlocking && !allowed {
			blocking = append(blocking, f)

			continue
		}
		warnings = append(warnings, f)
	}

	return blocking, warnings
}

// Messages joins finding messages for display.
func Messages(findings []Finding) string {
	msgs := make([]string, 0, len(findings))
	for _, f := range findings {
		msgs = append(msgs, f.Message)
	}

	return strings.Join(msgs, "; ")
}
