# Gostwriter

[![Test Status](https://github.com/jo-hoe/gostwriter/workflows/test/badge.svg)](https://github.com/jo-hoe/gostwriter/actions?workflow=test)
[![Lint Status](https://github.com/jo-hoe/gostwriter/workflows/lint/badge.svg)](https://github.com/jo-hoe/gostwriter/actions?workflow=lint)
[![Go Report Card](https://goreportcard.com/badge/github.com/jo-hoe/gostwriter)](https://goreportcard.com/report/github.com/jo-hoe/gostwriter)
[![Coverage Status](https://coveralls.io/repos/github/jo-hoe/gostwriter/badge.svg?branch=main)](https://coveralls.io/github/jo-hoe/gostwriter?branch=main)

image-to-markdown transcription and posting service

## Overview

Gostwriter provides an HTTP API to accept image uploads (PNG/JPEG), transcribe them to Markdown via a pluggable LLM client and post the resulting Markdown to a configured target.
By default, requests are processed synchronously and return `200 OK` with the result.
If the client sends `Prefer: respond-async`, the request is processed asynchronously and returns `202` with a `job_id` for status polling.

## Quick Start

- Prerequisites:
  - Docker (or Go 1.22+ if running from source)
  - GitHub Personal Access Token (PAT) with repo write access (for the GitHub target)
  - Optional: an OpenAI-compatible AI Proxy if using `llm.provider: aiproxy` (defaults to mock otherwise)

## Configure

- Copy `config.example.yaml` to either:
  - `dev/app-config.yaml` (used by docker-compose), or
  - `config.yaml` in the project root (used for local runs)
- Minimum edits:
  - Set `target.github.repositoryOwner`, `target.github.repositoryName`, `target.github.branch`
  - Provide `target.github.auth.token` (either paste the PAT or use `${GITHUB_TOKEN}`)
  - Choose LLM:
    - Mock (default): `llm.provider: "mock"` works without external services
    - AI Proxy: set `llm.provider: "aiproxy"`, `llm.aiproxy.baseUrl`, and `llm.aiproxy.apiKey` (or `${AIPROXY_API_KEY}`)
- Example snippet:

  ```yaml
  llm:
    provider: "mock"

  target:
    github:
      enabled: true
      repositoryOwner: "yourorg"
      repositoryName: "yourrepo"
      branch: "main"
      basePath: "inbox/"
      filenameTemplate: "{{ .Timestamp.Format \"20060102-150405\" }}-{{ .JobID }}.md"
      commitMessageTemplate: "Add transcription {{ .JobID }}"
      authorName: "Gostwriter Bot"
      authorEmail: "bot@example.com"
      apiBaseUrl: "https://api.github.com"
      auth:
        token: "${GITHUB_TOKEN}"
  ```

### Run

#### Using Docker Compose

- Place your config at `dev/app-config.yaml` (as above)
- Start:

```bash
docker compose up --build
```

- Health check:

```bash
curl http://localhost:8080/healthz
```

#### From source

- Ensure your config file is at `config.yaml` or set `GOSTWRITER_CONFIG` to its path
- Run:

```bash
go run ./cmd/gostwriter
```

#### Call the API

- Synchronous transcription (returns 200 on success):

```bash
curl -X POST "http://localhost:8080/v1/transcriptions" \
      -F "file=@/path/to/image.png" \
      -H "X-API-Key: YOUR_API_KEY"    # include only if apiKey is configured
```

- Asynchronous transcription (returns 202 with job_id):

```bash
curl -X POST "http://localhost:8080/v1/transcriptions" \
      -H "Prefer: respond-async" \
      -F "file=@/path/to/image.png" \
      -F "title=Meeting Notes" \
      -F "callback_url=https://example.com/hooks/gostwriter" \
      -F 'metadata={"source":"whiteboard","tags":["project-x"]}' \
      -H "X-API-Key: YOUR_API_KEY"    # include only if apiKey is configured
```

- Sample response:

```json
{ "job_id": "abcd-1234", "status_url": "/v1/transcriptions/abcd-1234" }
```

- Poll job status:

```bash
curl "http://localhost:8080/v1/transcriptions/abcd-1234"
```

- Stages: `queued` → `transcribing` → `posting` → `completed`
- On success, the status includes `target_result` with `location` and `commit` from the GitHub post

Notes:

- Required form field: `file` (PNG/JPEG)
- Optional fields: `title`, `metadata` (JSON object string), `callback_url` (HTTP(s) URL)
- Targets are fixed by server configuration; requests cannot override the target
- Max upload size defaults to 10 MiB (configurable)

## Configuration

Create a config.yaml in the project root or set GOSTWRITER_CONFIG to the path of your config file.
See config.example.yaml for a complete template.

## Security and behavior notes

- If server.apiKey is set, all API requests must include header X-API-Key.
- Temporary image files are always deleted:
  - If enqueue fails: deleted by request handler.
  - After processing: deleted by worker cleanup (async) or by request handler (sync).
