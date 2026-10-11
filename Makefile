BINARY := slk-mcp

.PHONY: build test fuzz mutate sync-label install sweep sweep-history hooks

## build: compile the server binary (version embedded via -ldflags)
build:
	go build -ldflags "-X main.version=$$(grep -m1 'version = ' main.go | sed -E 's/.*"([^"]+)".*/\1/')" -o $(BINARY) .

## test: run the full test suite (fuzz targets run their seed corpus)
test:
	go test ./...

## fuzz: explore every fuzz target for FUZZTIME each (default 30s). The
## parsers they cover read files and links anyone in the workspace can post.
FUZZTIME ?= 30s
fuzz:
	@for pkg in ./internal/tools ./internal/slack ./internal/format; do \
		for t in $$(go test $$pkg -list '^Fuzz' | grep '^Fuzz'); do \
			echo "== $$pkg $$t"; go test $$pkg -run '^$$' -fuzz "^$$t$$" -fuzztime $(FUZZTIME) || exit 1; \
		done; \
	done

## mutate: mutation testing with gremlins, default and extended mutators.
## Needs: go install github.com/go-gremlins/gremlins/cmd/gremlins@v0.5.0
GREMLINS ?= gremlins
GREMLINS_FLAGS := --workers 4 --test-cpu 2 --timeout-coefficient 15
GREMLINS_EXTENDED := --arithmetic-base=false --conditionals-boundary=false --conditionals-negation=false \
	--increment-decrement=false --invert-negatives=false --invert-logical --invert-loopctrl \
	--invert-assignments --remove-self-assignments --invert-bitwise --invert-bwassign
mutate:
	@for pkg in format slack digest config export lifecycle tools; do \
		$(GREMLINS) unleash $(GREMLINS_FLAGS) --threshold-efficacy 100 ./internal/$$pkg || exit 1; \
		$(GREMLINS) unleash $(GREMLINS_FLAGS) $(GREMLINS_EXTENDED) --threshold-efficacy 100 ./internal/$$pkg || exit 1; \
	done

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

## hooks: point git at .githooks/ so pre-push runs the full sweep. Run once
## per clone. The hook is tracked so it is reviewed like code; what it reads
## (.sweep-patterns.local) is not (ADR 076, ADR 083).
hooks:
	git config core.hooksPath .githooks
	@chmod +x .githooks/*
	@echo "hooks: core.hooksPath -> .githooks (pre-push runs sweep --history)"
