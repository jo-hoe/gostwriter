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

## Configuration

Create a config.yaml in the project root or set GOSTWRITER_CONFIG to the path of your config file.
See config.example.yaml for a complete template.

## Security and behavior notes

- If server.apiKey is set, all API requests must include header X-API-Key.
- Temporary image files are always deleted:
  - If enqueue fails: deleted by request handler.
  - After processing: deleted by worker cleanup (async) or by request handler (sync).
