---
name: implement
description: "Use when making any code change to this repo. Covers the full edit-lint-build-test loop with exact commands. Examples: 'implement this feature', 'fix this bug', 'add a new package'."
---

# Implement

Foundation for any code change in `luthersystems/svc`. Follow this loop for every change.

## Workflow

### 1. Understand the Change

- Read CLAUDE.md for project conventions and domain glossary
- Read existing code in the target package before modifying
- Check if the package has existing tests (`*_test.go`) to understand testing patterns

### 2. Create a Feature Branch

```bash
# Branch from main, use username/Description format
git checkout main && git pull
git checkout -b sam-at-luther/Short_description
```

### 3. Make Changes

- Follow existing code style in the package
- Add copyright header to new files: `// Copyright © <year> Luther Systems, Ltd. All right reserved.`
- If adding a new package, create it at the top level (flat package structure)
- Do NOT edit generated code in `oracle/testservice/gen/`

### 4. Format & Lint

```bash
# Format
gofmt -w .

# Lint (matches CI — gosec enabled)
golangci-lint run
```

Fix any lint issues before proceeding. The `gosec` linter is enabled — pay attention to security findings.

### 5. Build

```bash
go build ./...
```

### 6. Test

```bash
# Run tests for the changed package
go test -timeout 10m ./your-package/...

# Run the full test suite
go test -timeout 10m ./...
```

If working on `oracle/` tests, the substrate plugin must be available:
```bash
# Download the plugin (only needed once)
./scripts/obtain-plugin.sh
# Or via make
make plugin
```

### 7. Verify Round-Trip

If your change adds a new public API, verify it with a simple test. If modifying behavior, write a test that captures the expected behavior first (TDD).

## Key Reminders

- Use `testify` for assertions in tests (existing pattern in codebase)
- The `svcerr` package maps errors to gRPC status codes — use it for error handling
- Context propagation uses `grpclogging.AddLogrusField(ctx, key, value)` for structured logging
- Oracle tests need `SUBSTRATEHCP_FILE` env var pointing to the plugin binary (set by Makefile)

## Checklist

- [ ] Code builds cleanly (`go build ./...`)
- [ ] Lint passes (`golangci-lint run`)
- [ ] Tests pass for changed packages
- [ ] Full test suite passes (`go test -timeout 10m ./...`)
- [ ] New files have copyright header
- [ ] No generated files were manually edited
