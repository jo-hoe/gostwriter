package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	// Busy timeout to avoid SQLITE_BUSY in concurrent access.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		image_path TEXT NOT NULL,
		mime_type TEXT NOT NULL,
		target_name TEXT NOT NULL,
		callback_url TEXT,
		title TEXT,
		metadata_json TEXT,
		stage TEXT NOT NULL,
		error_message TEXT,
		target_location TEXT,
		target_commit TEXT,
		created_at TEXT NOT NULL,
		started_at TEXT,
		completed_at TEXT
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CreateJob(job *Job) error {
	if job == nil {
		return errors.New("job is nil")
	}
	if job.ID == "" {
		return errors.New("job.ID is required")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	meta := ""
	if job.Metadata != nil {
		b, err := json.Marshal(job.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata: %w", err)
		}
		meta = string(b)
	}
	var cb *string
	if job.CallbackURL != nil && *job.CallbackURL != "" {
		cb = job.CallbackURL
	}
	var title *string
	if job.Title != nil && *job.Title != "" {
		title = job.Title
	}

	_, err := s.db.Exec(
		`INSERT INTO jobs (id, image_path, mime_type, target_name, callback_url, title, metadata_json, stage, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.ImagePath, job.MimeType, job.TargetName, cb, title, meta, string(job.Stage), job.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateStage(id string, stage Stage, startedAt *time.Time) error {
	var started *string
	if startedAt != nil {
		ts := startedAt.UTC().Format(time.RFC3339Nano)
		started = &ts
	}
	// Update stage and optionally started_at (only set when provided).
	if started != nil {
		_, err := s.db.Exec(`UPDATE jobs SET stage = ?, started_at = ? WHERE id = ?`, string(stage), *started, id)
		if err != nil {
			return fmt.Errorf("update stage: %w", err)
		}
		return nil
	}
	_, err := s.db.Exec(`UPDATE jobs SET stage = ? WHERE id = ?`, string(stage), id)
	if err != nil {
		return fmt.Errorf("update stage: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveResult(id string, location, commit string, completedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE jobs
		SET target_location = ?, target_commit = ?, stage = ?, error_message = NULL, completed_at = ?
		WHERE id = ?`,
		location, commit, string(StageCompleted), completedAt.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("save result: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveError(id string, errMsg string, completedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE jobs
		SET error_message = ?, stage = ?, completed_at = ?
		WHERE id = ?`,
		errMsg, string(StageFailed), completedAt.UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("save error: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetJob(id string) (*Job, error) {
	row := s.db.QueryRow(`SELECT id, image_path, mime_type, target_name, callback_url, title, metadata_json, stage,
		error_message, target_location, target_commit, created_at, started_at, completed_at
		FROM jobs WHERE id = ?`, id)

	var job Job
	var cb, title, meta, errMsg, loc, commit, created, started, completed sql.NullString
	var stage string

	if err := row.Scan(
		&job.ID,
		&job.ImagePath,
		&job.MimeType,
		&job.TargetName,
		&cb,
		&title,
		&meta,
		&stage,
		&errMsg,
		&loc,
		&commit,
		&created,
		&started,
		&completed,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("job not found")
		}
		return nil, fmt.Errorf("scan job: %w", err)
	}

	if cb.Valid {
		v := cb.String
		job.CallbackURL = &v
	}
	if title.Valid {
		v := title.String
		job.Title = &v
	}
	if meta.Valid && meta.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(meta.String), &m); err == nil {
			job.Metadata = m
		} else {
			// Leave Metadata nil on error; do not fail retrieval.
			job.Metadata = nil
		}
	}
	if errMsg.Valid {
		v := errMsg.String
		job.ErrorMessage = &v
	}
	if loc.Valid {
		v := loc.String
		job.TargetLocation = &v
	}
	if commit.Valid {
		v := commit.String
		job.TargetCommit = &v
	}
	if created.Valid {
		if t, err := time.Parse(time.RFC3339Nano, created.String); err == nil {
			job.CreatedAt = t
		}
	}
	if started.Valid {
		if t, err := time.Parse(time.RFC3339Nano, started.String); err == nil {
			job.StartedAt = &t
		}
	}
	if completed.Valid {
		if t, err := time.Parse(time.RFC3339Nano, completed.String); err == nil {
			job.CompletedAt = &t
		}
	}
	job.Stage = Stage(stage)

	return &job, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}