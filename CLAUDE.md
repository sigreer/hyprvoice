# Hyprvoice Development Guidelines

## Version Management

**Increment the version for every push.** The version is set at build time via ldflags in `cmd/hyprvoice/main.go`.

Current version: **0.2.1**

Use semantic versioning:
- Patch (0.2.0 → 0.2.1): Bug fixes
- Minor (0.2.0 → 0.3.0): New features
- Major (0.2.0 → 1.0.0): Breaking changes

## Build Requirements

**After any code change, recompile the binary with the new version and place it at the repo root:**

```bash
go build -ldflags "-X main.version=X.Y.Z" -o hyprvoice ./cmd/hyprvoice
```

Replace `X.Y.Z` with the incremented version. The binary must be available at the repo root before committing.

Verify the version:
```bash
./hyprvoice version
```

## Project Structure

- `cmd/hyprvoice/main.go` - CLI entry point (cobra commands, version variable)
- `internal/bus/` - IPC socket communication
- `internal/daemon/` - Main daemon logic
- `internal/config/` - Configuration management
- `internal/recording/` - Audio recording via PipeWire
- `internal/transcriber/` - Speech-to-text adapters (OpenAI, Groq)
- `internal/injection/` - Text injection backends (wtype, ydotool, clipboard)
- `internal/llm/` - LLM post-processing
- `internal/pipeline/` - Audio processing pipeline
- `internal/notify/` - Desktop notifications

## Testing

Run tests before committing:
```bash
go test ./...
```
