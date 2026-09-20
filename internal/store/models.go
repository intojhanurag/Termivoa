package store

import "time"

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
