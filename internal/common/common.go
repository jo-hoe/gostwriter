package common

// Shared constants to enforce DRY and avoid magic strings/numbers.

// HTTP headers and content types
const (
	HeaderAPIKey       = "X-API-Key" // #nosec G101 - header name constant, not a credential
	HeaderPrefer       = "Prefer"
	PreferRespondAsync = "respond-async"
	ContentTypeJSON    = "application/json"
)

// API paths
const (
	PathHealthz        = "/healthz"
	PathTranscriptions = "/v1/transcriptions"
)

// Defaults and limits
const (
	DefaultQueueCapacity = 128
	DefaultWorkerCount   = 4
	SQLiteBusyTimeoutMS  = 5000
)

// Git related constants
const (
	GitExecutable = "git"
	GitRemoteName = "origin"
)

// MIME types
const (
	MimeImagePNG  = "image/png"
	MimeImageJPEG = "image/jpeg"
	MimeImageJPG  = "image/jpg"
)

// Subdirectory names
const (
	UploadsDirName = "uploads"
	ReposDirName   = "repos"
)

// Callback status strings
const (
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)
