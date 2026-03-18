# Whisper API server (TLR-compatible)

OpenAI-compatible transcription server for Thinline Radio. Optionally calls Ollama to add a short **summary** for alerts. If Ollama is disabled or unavailable, transcription runs as normal and the response omits `summary` (no deadlock).

## Endpoints

- `GET /health` — health check (TLR uses this to detect the server).
- `POST /v1/audio/transcriptions` — multipart form: `file`, `model`, `language`, `prompt`, `response_format` (e.g. `verbose_json`). Returns `text`, `language`, `duration`, `segments`, and optionally `summary` when Ollama succeeds.

## Setup

```bash
cd whisper
pip install -r requirements.txt
```

## Run (transcription only)

```bash
# Default: Whisper "base" model, no Ollama
uvicorn whisper_server:app --host 0.0.0.0 --port 8000
```

## Run with optional Ollama summary

Set env vars so the server tries to summarize each transcript with Ollama. If Ollama is down or times out, the request still returns the transcript without `summary`.

```bash
export OLLAMA_ENABLED=1
export OLLAMA_URL=http://localhost:11434   # default
export OLLAMA_MODEL=llama3.2               # default
export OLLAMA_TIMEOUT=45                   # seconds; avoid waiting forever
export SUMMARY_PROMPT="Summarize this dispatch transcript in 1-2 short sentences. Be concise."

uvicorn whisper_server:app --host 0.0.0.0 --port 8000
```

- **OLLAMA_ENABLED**: `1` to attempt summary; `0` or unset = transcription only.
- **OLLAMA_TIMEOUT**: Max seconds to wait for Ollama. If it fails or times out, the response has no `summary` and the request does not block.

## Whisper model

Default model is `base`. Override with:

```bash
export WHISPER_MODEL=small   # or tiny, base, small, medium, large
uvicorn whisper_server:app --host 0.0.0.0 --port 8000
```

## TLR configuration

In TLR Admin → Transcription: set provider to **Whisper API** and **Whisper API URL** to `http://localhost:8000` (or this server’s host). TLR will send audio here and use the returned `text` and, when present, `summary` for alerts.
