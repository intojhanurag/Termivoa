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
}
