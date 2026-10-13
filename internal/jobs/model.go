// Package jobs defines the job model and store interface for transcription lifecycle tracking.
package jobs

import (
	"context"
	"time"
)

// Stage represents the lifecycle stage of a transcription job.
type Stage string

// Stage constants describe the lifecycle phases of a transcription job.
const (
	StageQueued       Stage = "queued"
	StageTranscribing Stage = "transcribing"
	StagePosting      Stage = "posting"
	StageCompleted    Stage = "completed"
	StageFailed       Stage = "failed"
)

// Job describes a single transcription and posting request.
type Job struct {
	ID             string         // UUIDv4
	ImagePath      string         // absolute or storage-relative path to the uploaded image (temporary)
	MimeType       string         // image mime (image/png, image/jpeg)
	TargetName     string         // configured target name to post to
	CallbackURL    *string        // optional callback
	Metadata       map[string]any // optional arbitrary metadata
	Stage          Stage          // current stage
	ErrorMessage   *string        // last error, if any
	TargetLocation *string        // result location string from target (e.g., path in repo)
	TargetCommit   *string        // resulting commit hash if target supports it
	CreatedAt      time.Time      // creation time
	StartedAt      *time.Time     // when processing actually started
	CompletedAt    *time.Time     // when finished (success or failure)
}

// TargetResult represents the posting outcome returned by a target.
type TargetResult struct {
	TargetName string // e.g., "docs-main"
	Location   string // e.g., "git:repo@branch:path/file.md"
	Commit     string // commit hash if applicable
}

// Store defines persistence for Jobs and their lifecycle.
type Store interface {
	CreateJob(ctx context.Context, job *Job) error
	UpdateStage(ctx context.Context, id string, stage Stage, startedAt *time.Time) error
	SaveResult(ctx context.Context, id string, location, commit string, completedAt time.Time) error
	SaveError(ctx context.Context, id string, errMsg string, completedAt time.Time) error
	GetJob(ctx context.Context, id string) (*Job, error)
	Close() error
}
