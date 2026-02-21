Gostwriter: async image-to-markdown transcription and posting service

Overview
Gostwriter provides an HTTP API to accept image uploads (PNG/JPEG), transcribe them to Markdown via a pluggable LLM client (mock included), and post the resulting Markdown to a configured target. By default, requests are processed synchronously and return 200 OK with the result. If the client sends Prefer: respond-async, the request is processed asynchronously and returns 202 with a job_id for status polling. The current target is a single Git repository (via system git CLI). The service is designed to be extensible for future targets (e.g., OneDrive, email).

Key features
- Sync by default, async on request:
  - Default: synchronous processing returns result with 200 OK
  - Async when header Prefer: respond-async is present; returns 202 with job_id
- Status endpoint to query current stage and result
- Optional callback URL invoked on completion/failure
- Config via YAML (lower camelCase keys)
- Git target using HTTPS + PAT (via system git)
- Mock LLM implementation; easy to extend with real providers
- Temporary image files are always deleted (not persisted)
- Job metadata stored in SQLite via modernc.org/sqlite
- Minimal dependencies: YAML and SQLite only; otherwise stdlib

Requirements
- Go 1.25+
- Git installed and available on PATH (for git target)
- A Git repository and a Personal Access Token (PAT) for HTTPS operations
- Windows or Linux

Configuration
Create a config.yaml in the project root or set GOSTWRITER_CONFIG to the path of your config file. See config.example.yaml for a complete template.

Notes:
- Environment variables in config are expanded (e.g., ${GIT_TOKEN}).
- Durations are Kubernetes-style (e.g., 2s, 4m, 5h).
- Sizes are Kubernetes-like for binary units Ki, Mi, Gi (e.g., 10Mi). Decimal KB/MB/GB are also supported.

Example
server:
  addr: ":8080"
  readTimeout: 15s
  writeTimeout: 30s
  idleTimeout: 60s
  maxUploadSize: 10Mi
  workerCount: 4
  storageDir: "data"
  apiKey: ""           # optional, header: X-API-Key
  databasePath: ""     # defaults to storageDir/gostwriter.db
  shutdownGrace: 15s
  callbackRetries: 3
  callbackBackoff: 2s

llm:
  provider: "mock"
  mock:
    delay: 2s
    prefix: "Transcribed by Mock"

# Single target configuration:
target:
  type: "git"
  name: "docs-main"
  repoUrl: "https://github.com/yourorg/yourrepo.git"
  branch: "main"
  basePath: "inbox/"
  filenameTemplate: "{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md"
  commitMessageTemplate: "Add transcription {{ .JobID }}"
  authorName: "Gostwriter Bot"
  authorEmail: "bot@example.com"
  cloneCacheDir: ""
  auth:
    type: "basic"
    username: "git"
    token: "${GIT_TOKEN}"

Build and run
- Build: go build -o gostwriter ./cmd/gostwriter
- Run with default config file name (config.yaml): ./gostwriter
- Or run with explicit config: set GOSTWRITER_CONFIG=path\to\config.yaml and start the binary.

HTTP API
Health
- GET /healthz -> {"status":"ok"}

Create transcription
- POST /v1/transcriptions (multipart/form-data)
  Headers:
  - Optional: Prefer: respond-async to request asynchronous handling
  - Optional: X-API-Key when configured in server.apiKey
  Fields:
  - file: required; image/png or image/jpeg
  - callback_url: optional; URL to POST when completed or failed
  - title: optional; used as Markdown H1 header
  - metadata: optional; JSON object as string (e.g., {"category":"notes"})

  Behavior:
  - Default synchronous (no Prefer: respond-async header):
    - Processes upload immediately (transcribe + post), returns 200 OK with result body:
      {
        "job_id": "uuid",
        "stage": "completed",
        "created_at": "...",
        "started_at": "...",
        "completed_at": "...",
        "error": null,
        "target_result": {
          "target": "docs-main",
          "location": "git:https://...@main:inbox/20260101-120000-<id>.md",
          "commit": "abc123..."
        }
      }
  - Asynchronous (Prefer: respond-async present):
    - Enqueues background job and returns 202 Accepted:
      {
        "job_id": "uuid",
        "status_url": "/v1/transcriptions/{uuid}"
      }

Status
- GET /v1/transcriptions/{id}
  Response: 200 OK
  {
    "job_id": "uuid",
    "stage": "queued|transcribing|posting|completed|failed",
    "created_at": "...",
    "started_at": "...",
    "completed_at": "...",
    "error": "string or null",
    "target_result": {
      "target": "docs-main",
      "location": "git:https://...@main:inbox/20260101-120000-<id>.md",
      "commit": "abc123..."
    }
  }
  404 if job not found.

Callback payload
When callback_url is provided in the create request, Gostwriter sends:
POST {callback_url}
Content-Type: application/json
{
  "job_id": "uuid",
  "status": "completed|failed",
  "stage": "completed|failed",
  "error": "string or null",
  "result": {
    "target": "docs-main",
    "location": "git:https://...@main:inbox/....md",
    "commit": "abc123..."
  }
}
Retries and backoff controlled by server.callbackRetries and server.callbackBackoff.

Examples (PowerShell)
- Set GIT_TOKEN and run:
  $env:GIT_TOKEN="ghp_xxx"
  .\gostwriter.exe

- Synchronous upload (default):
  $form = @{
    file = Get-Item "C:\path\to\image.jpg"
    title = "Meeting Notes"
    metadata = '{"category":"meetings"}'
  }
  Invoke-RestMethod -Method Post -Uri http://localhost:8080/v1/transcriptions -Form $form

- Asynchronous upload:
  Invoke-WebRequest -Method Post -Uri http://localhost:8080/v1/transcriptions `
    -Headers @{ "Prefer" = "respond-async" } `
    -Form $form

- Check status:
  Invoke-RestMethod -Method Get -Uri http://localhost:8080/v1/transcriptions/1e2d3c4b-.....

Security and behavior notes
- If server.apiKey is set, all API requests must include header X-API-Key.
- Temporary image files are always deleted:
  - If enqueue fails: deleted by request handler.
  - After processing: deleted by worker cleanup (async) or by request handler (sync).
- Job metadata persists in SQLite. It is easy to swap Store implementation (e.g., Redis) later.

Extensibility
- LLM providers: implement interface internal/llm.Client and wire in main based on cfg.LLM.Provider.
- Targets: implement interface internal/targets.Target and register in the target registry.
- Git target uses system git via os/exec to keep dependencies minimal.

Architecture summary
- cmd/gostwriter: program entry, wiring, graceful shutdown
- internal/config: YAML loader with defaults, env expansion, and validation (lower camelCase keys)
- internal/server: HTTP routing, handlers, middleware
- internal/jobs: job model, SQLite store, in-memory queue and worker pool orchestration
- internal/llm: interface and mock client
- internal/targets: interface, registry, git target implementation (via system git)
- internal/storage: upload handler for temp image storage
- internal/processor: worker implementing the transcription + posting pipeline

License
Apache-2.0 or similar (add your chosen license)