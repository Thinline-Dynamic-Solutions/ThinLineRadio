"""
OpenAI-compatible Whisper API server with optional Ollama summary.
TLR calls POST /v1/audio/transcriptions; if Ollama is enabled and available,
response includes "summary" for alerts. If Ollama is unavailable, returns
transcript only (no deadlock).
"""
import os
import tempfile
import time
from pathlib import Path

import httpx
import whisper
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse

app = FastAPI(title="Whisper API + Optional Ollama Summary")

# Config: set OLLAMA_ENABLED=1 to try summary; OLLAMA_URL (default http://localhost:11434); OLLAMA_MODEL (default llama3.2); SUMMARY_PROMPT
OLLAMA_ENABLED = os.environ.get("OLLAMA_ENABLED", "0").strip().lower() in ("1", "true", "yes")
OLLAMA_URL = os.environ.get("OLLAMA_URL", "http://localhost:11434").rstrip("/")
OLLAMA_MODEL = os.environ.get("OLLAMA_MODEL", "llama3.2")
OLLAMA_TIMEOUT = float(os.environ.get("OLLAMA_TIMEOUT", "45"))  # seconds; avoid deadlock
SUMMARY_PROMPT = os.environ.get(
    "SUMMARY_PROMPT",
    "Summarize this dispatch transcript in 1-2 short sentences. Be concise.",
)

# Load Whisper once at startup (same process = same GPU; Ollama runs after Whisper, no contention)
WHISPER_MODEL_NAME = os.environ.get("WHISPER_MODEL", "base")
_whisper_model = None


def get_whisper_model():
    global _whisper_model
    if _whisper_model is None:
        _whisper_model = whisper.load_model(WHISPER_MODEL_NAME)
    return _whisper_model


def summarize_with_ollama(transcript: str) -> str | None:
    """Call Ollama to summarize transcript. Returns None on any failure (no deadlock)."""
    if not transcript or not OLLAMA_ENABLED:
        return None
    prompt = f"{SUMMARY_PROMPT}\n\n{transcript}"
    payload = {"model": OLLAMA_MODEL, "prompt": prompt, "stream": False}
    try:
        with httpx.Client(timeout=OLLAMA_TIMEOUT) as client:
            r = client.post(f"{OLLAMA_URL}/api/generate", json=payload)
            r.raise_for_status()
            data = r.json()
            summary = (data.get("response") or "").strip()
            if summary:
                return summary[:500]
            return None
    except Exception:
        # Timeout, connection error, or JSON error: skip summary, do not block
        return None


@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/v1/audio/transcriptions")
async def transcriptions(
    file: UploadFile = File(...),
    model: str = Form("whisper-1"),
    language: str | None = Form(None),
    prompt: str | None = Form(None),
    response_format: str = Form("verbose_json"),
    temperature: float = Form(0.0),
):
    if not file.filename:
        raise HTTPException(status_code=400, detail="file required")
    suffix = Path(file.filename).suffix or ".m4a"
    with tempfile.NamedTemporaryFile(suffix=suffix, delete=False) as tmp:
        try:
            content = await file.read()
            tmp.write(content)
            tmp.flush()
            wp = get_whisper_model()
            kwargs = {"fp16": False}
            if language and language != "auto":
                kwargs["language"] = language
            if prompt:
                kwargs["initial_prompt"] = prompt
            if temperature > 0:
                kwargs["temperature"] = temperature
            result = wp.transcribe(tmp.name, **kwargs)
        finally:
            try:
                os.unlink(tmp.name)
            except OSError:
                pass

    text = (result.get("text") or "").strip()
    lang = result.get("language", "en")
    segments_in = result.get("segments") or []
    duration = 0.0
    if segments_in:
        duration = max(s.get("end", 0) or 0 for s in segments_in)

    segments_out = [
        {"id": i, "start": s.get("start", 0), "end": s.get("end", 0), "text": (s.get("text") or "").strip()}
        for i, s in enumerate(segments_in)
        if (s.get("text") or "").strip()
    ]
    if not segments_out and text:
        segments_out = [{"id": 0, "start": 0, "end": duration, "text": text}]

    # Optional summary via Ollama (best-effort; never block)
    summary = summarize_with_ollama(text)

    body = {
        "text": text,
        "language": lang,
        "duration": duration,
        "segments": segments_out,
    }
    if summary:
        body["summary"] = summary

    if response_format == "json":
        return JSONResponse(content={"text": text, **({"summary": summary} if summary else {})})
    return JSONResponse(content=body)
