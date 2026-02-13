# CLAUDE.md — luthersystems/svc

## Overview

A modular Go 1.23 service framework for the [Luther Platform](https://luthersystems.com). Provides composable building blocks for middleware services: REST/gRPC gateway orchestration ("oracle" pattern), pluggable cloud storage (S3/Azure Blob), Handlebars templating with ELPS scripting, OpenTelemetry tracing, Prometheus metrics, AWS SES email, and HTTP middleware. Single Go module at `github.com/luthersystems/svc`.

## Repository Layout

| Directory | Purpose |
|---|---|
| `oracle/` | Core service framework — REST/gRPC gateway, phylum integration, config, test helpers, fake IDP |
| `oracle/testservice/` | Test service with proto definitions (buf), phylum scripts, generated gRPC/gateway code |
| `docstore/` | Document storage interface (`DocStore`, `Getter`, `Putter`, `Deleter`) |
| `docstore/s3/` | AWS S3 storage backend |
| `docstore/azblob/` | Azure Blob Storage backend |
| `grpclogging/` | gRPC unary interceptors, structured logging (logrus), request ID propagation |
| `midware/` | HTTP middleware framework (`Middleware` interface, `Chain`, trace headers) |
| `svcerr/` | Centralized error handling, gRPC status code mapping, exception factories |
| `libhandlebars/` | Handlebars templating engine (Go port via `luthersystems/raymond`) with ELPS integration |
| `libdates/` | Civil date difference calculator — O(1) YMD algorithm |
| `mailer/` | AWS SES email sender with attachment support |
| `opttrace/` | OpenTelemetry tracer wrapper with OTLP exporter support |
| `logmon/` | Prometheus log monitoring hooks |
| `reqarchive/` | HTTP request archiving middleware (S3 backend) |
| `static/` | Static asset serving, embedded filesystem support |
| `protos/` | Proto helper utilities (`RemoveSensitiveFields`) |
| `txctx/` | Transaction-scoped context management |
| `scripts/` | Build/utility scripts (plugin download) |
| `build/` | Build artifacts (gitignored) |

## Build & Test Commands

```bash
# Run all tests (what CI runs)
make citest

# Run Go tests only (no plugin download)
go test -timeout 10m ./...

# Run a single package's tests
go test -timeout 10m ./libdates/...

# Run a single test
go test -timeout 10m -run TestDiffYMD ./libdates/...

# Lint (matches CI config)
golangci-lint run

# Download substrate plugin (required for oracle tests)
./scripts/obtain-plugin.sh

# Build plugin
make plugin
```

## CI Pipeline

Single GitHub Actions workflow (`.github/workflows/svc.yml`):
- **Triggers**: PRs targeting `main` only
- **Runner**: `ubuntu-22.04`, Go 1.23
- **Steps**: checkout → setup-go → clean modcache → golangci-lint (v1.63) → `make citest`
- **Linter config**: `.golangci.yml` — gosec enabled, 2m timeout
- **No deploy/release automation** — this is a library repo

## Branch Protection

- 1 approving review required
- Stale reviews dismissed on new pushes
- All PR conversations must be resolved
- Admins subject to protection rules
- `sam-at-luther` can bypass PR requirements

## Conventions

### Git

- **Branch naming**: `username/Description_with_underscores` (e.g., `sam-at-luther/Fix_version_bug`) or descriptive kebab-case (e.g., `add-libdates`)
- **Commit messages**: Imperative mood, short subject line. Common prefixes: Add, Fix, Bump, Use, Improve, Remove. Not conventional commits.
- **Versioning**: Semver tags (currently v0.14.x), patch releases as needed
- **PRs**: One logical change per PR, typically small-medium (1-12 files). PRs to `main` only.

### Code

- Copyright header: `// Copyright © <year> Luther Systems, Ltd. All right reserved.`
- Go module: `github.com/luthersystems/svc`
- Test framework: standard `testing` + `github.com/stretchr/testify`
- Error handling: `svcerr` package maps errors to gRPC status codes
- Logging: `logrus` structured logging throughout
- Context propagation: request IDs via `x-request-id` header, logrus fields via context

## Domain Glossary

| Term | Meaning |
|---|---|
| **Oracle** | The service orchestration pattern — REST/gRPC gateway that routes to phylum business logic |
| **Phylum** | Business logic layer written in ELPS (a Lisp dialect), executed by the substrate runtime |
| **Substrate** | The Luther Platform runtime that executes phylum scripts (external binary, downloaded as plugin) |
| **SubstrateHCP** | The substrate plugin binary (`substratehcp-{os}-{arch}-{version}`) |
| **ELPS** | Extensible Lisp-like Programming System — the scripting language for phylums |
| **DocStore** | Abstract document storage interface with S3 and Azure Blob backends |
| **Civil date** | A date without time-of-day or timezone — year/month/day only (used in `libdates`) |

## Skills

| Skill | Purpose |
|---|---|
| `implement` | Foundation for any code change — edit, format, lint, build, test loop |
| `verify` | Local CI mirror — run all checks before pushing |
| `pr` | Ship changes — verify, push, create PR with repo conventions |
| `pickup-issue` | Full issue lifecycle — read issue, branch, implement, verify, PR |
| `release` | Tag a new semver release on main |

## Common Pitfalls

- **Plugin required for oracle tests**: Tests in `oracle/` need the substrate plugin binary. Run `./scripts/obtain-plugin.sh` or `make plugin` first. CI does this via `make citest`.
- **SUBSTRATEHCP_FILE env var**: Must point to the plugin binary. Set automatically by the Makefile.
- **Go module cache**: CI cleans the module cache before linting (`go clean -modcache`) to avoid golangci-lint issues.
- **gosec linter**: Enabled in CI — security-sensitive code will be flagged. Check `.golangci.yml`.
- **`script -q -e -c` wrapper**: CI wraps `make citest` in a pseudo-TTY. If reproducing CI locally, run `make citest` directly.
- **Generated code**: Do not edit files in `oracle/testservice/gen/` — these are generated by `buf generate`.
