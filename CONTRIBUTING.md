# Contributing to slk-mcp

Thanks for your interest in contributing!

## Quick start

```bash
git clone https://github.com/velesnitski/slk-mcp.git
cd slk-mcp
go build -o slk-mcp .
```

## Development workflow

1. Fork the repo and create a branch from `dev`
2. Make your changes
3. Run `go build` and `go vet ./...`
4. Open a PR against `dev`

## Code style

- Go 1.23+
- Run `gofmt` before committing
- Keep tool handlers in the appropriate file under `internal/tools/`
- Use `mcp.NewToolResultError()` for user-facing errors, `error` return for system errors
- No sensitive data in code, tests, or docs
