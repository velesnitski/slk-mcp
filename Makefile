BINARY := slk-mcp

.PHONY: build test sync-label install sweep sweep-history

## build: compile the server binary (version embedded via -ldflags)
build:
	go build -ldflags "-X main.version=$$(grep -m1 'version = ' main.go | sed -E 's/.*"([^"]+)".*/\1/')" -o $(BINARY) .

## test: run the full test suite
test:
	go test ./...

## sync-label: rename the slk-mcp key in ~/.claude.json to "slack v<version>"
## so the /mcp dialog shows the running version (the dialog uses the config
## key, not the server-reported name). Idempotent; keeps a .bak.
sync-label:
	python3 scripts/sync-mcp-label.py

## install: build then sync the /mcp label. Run this after a version bump,
## then RESTART Claude Code (a /mcp reconnect is NOT enough — the dialog
## label is read from the config key at session start). Kept separate from
## `build` so plain builds never touch ~/.claude.json.
install: build sync-label

sweep:
	./scripts/sweep.sh

sweep-history:
	./scripts/sweep.sh --history
