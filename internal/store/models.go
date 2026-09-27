package store

import "time"

type OperationKind string

const (
	OperationSessionCreate    OperationKind = "session_create"
	OperationSessionRename    OperationKind = "session_rename"
	OperationSessionTerminate OperationKind = "session_terminate"
)

type Platform string

const (
	PlatformDarwin  Platform = "darwin"
	PlatformWindows Platform = "windows"
	PlatformWSL     Platform = "wsl"
)

type SessionState string

const (
	SessionStarting SessionState = "starting"
	SessionRunning  SessionState = "running"
	SessionExiting  SessionState = "exiting"
	SessionExited   SessionState = "exited"
	SessionLost     SessionState = "lost"
)

type Project struct {
	ID            string
	DisplayName   string
	CanonicalPath string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DisabledAt    *time.Time
}

type ShellProfile struct {
	ID             string
	DisplayName    string
	Platform       Platform
	ExecutablePath string
	Arguments      []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DisabledAt     *time.Time
}

type SessionMetadata struct {
	ID               string
	Generation       string
	BrokerGeneration string
	Title            string
	TitleCustom      bool // Set once a user renamed the session.
	ProjectID        string
	ShellProfileID   string
	State            SessionState
	Columns          int
	Rows             int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	StartedAt        *time.Time
	EndedAt          *time.Time
	ExitCode         *int32
}

// MutationResult is the durable, deterministic acknowledgement for a
// broker-owned session mutation. It deliberately contains no terminal data or
// live runtime capability.
type MutationResult struct {
	OperationID string
	Kind        OperationKind
	SessionID   string
	Generation  string
	Title       string
	State       SessionState
	CommittedAt time.Time
	Session     MutationSessionSnapshot
}

// MutationSessionSnapshot is the exact bounded session view returned when a
// mutation commits. Nullable lifecycle fields use explicit presence bits so
// receipts remain comparable in tests and deterministic across SQLite reopen.
type MutationSessionSnapshot struct {
	ID               string       `json:"id"`
	Generation       string       `json:"generation"`
	BrokerGeneration string       `json:"brokerGeneration"`
	Title            string       `json:"title"`
	TitleCustom      bool         `json:"titleCustom,omitempty"`
	ProjectID        string       `json:"projectId"`
	ShellProfileID   string       `json:"shellProfileId"`
	State            SessionState `json:"state"`
	Columns          int          `json:"columns"`
	Rows             int          `json:"rows"`
	CreatedAt        time.Time    `json:"createdAt"`
	UpdatedAt        time.Time    `json:"updatedAt"`
	StartedAt        time.Time    `json:"startedAt"`
	HasStartedAt     bool         `json:"hasStartedAt"`
	EndedAt          time.Time    `json:"endedAt"`
	HasEndedAt       bool         `json:"hasEndedAt"`
	ExitCode         int32        `json:"exitCode"`
	HasExitCode      bool         `json:"hasExitCode"`
}
