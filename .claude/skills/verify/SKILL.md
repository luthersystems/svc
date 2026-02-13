---
name: verify
description: "Local CI mirror. Run all checks that CI runs before pushing. Use before creating a PR or when you want to validate your changes match CI. Examples: 'verify my changes', 'run CI checks locally', 'check if this will pass CI'."
---

# Verify

Run every check that CI runs, in order. This is a local mirror of `.github/workflows/svc.yml`.

## Workflow

### 1. Lint (golangci-lint v1.63, gosec enabled)

```bash
golangci-lint run
```

This uses `.golangci.yml` which enables `gosec` with a 2-minute timeout. All default linters are also active.

### 2. Download Substrate Plugin

```bash
# Only needed if not already present
make plugin
```

This downloads the `substratehcp` binary from `download.luthersystemsapp.com` for both linux and darwin. Required for oracle package tests.

### 3. Run Full Test Suite

```bash
# Matches CI exactly: make citest = plugin + go-test
go test -timeout 10m ./...
```

### 4. Build Check

```bash
go build ./...
```

## Quick Verify (Skip Plugin)

If you haven't changed anything in `oracle/`, you can skip the plugin download:

```bash
golangci-lint run && go test -timeout 10m ./... && go build ./...
```

## Key Reminders

- CI runs on `ubuntu-22.04` with Go 1.23 — ensure compatibility
- CI cleans the module cache before linting (`go clean -modcache`) — if you hit cache issues locally, do the same
- The `SUBSTRATEHCP_FILE` env var must point to the plugin binary for oracle tests
- CI wraps `make citest` in `script -q -e -c` (pseudo-TTY) — this shouldn't affect local runs

## Checklist

- [ ] golangci-lint passes (0 issues)
- [ ] All tests pass with 10m timeout
- [ ] Build succeeds
- [ ] No uncommitted generated file changes
