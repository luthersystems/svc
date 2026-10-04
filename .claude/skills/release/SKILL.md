---
name: release
description: "Create a new GitHub release with version tag and release notes. Determines the next version, verifies main, tags, and creates a GitHub release. Examples: 'create a release', 'release v0.15.0', 'cut a new version'."
---

# Release

Create a versioned GitHub release on main with auto-generated release notes.

## Workflow

### 1. Check Current Version

```bash
git tag --sort=-v:refname | head -5
```

### 2. Ensure Main is Clean

```bash
git checkout main && git pull
git status  # Must be clean
```

### 3. Run Verify

Run the `verify` skill on main. Do not release if any checks fail.

### 4. Determine Next Version

This repo follows semver `v0.MINOR.PATCH`:
- **Patch bump** (v0.14.16 → v0.14.17): Bug fixes, dependency bumps, small improvements
- **Minor bump** (v0.14.x → v0.15.0): New packages, breaking API changes, significant features

Most releases are patch bumps. Ask the user if unclear.

### 5. Generate Release Notes

Review commits since the last release to build release notes:

```bash
git log --oneline <previous-tag>..HEAD
```

Organize into categories based on commit prefixes:
- **Features** — `Add` commits (new packages, capabilities)
- **Fixes** — `Fix` commits (bug fixes)
- **Dependencies** — `Bump` commits (dependency updates)
- **Improvements** — `Improve`, `Use`, `Remove` commits (refactors, enhancements)

### 6. Create Tag and GitHub Release

Cut the release with the `Release (tag)` workflow (`.github/workflows/release-tag.yml`). Never run `git tag` and `git push origin vX.Y.Z`: agent sandboxes cannot push tags, and the workflow is the one release path.

1. Dispatch a dry run on `main`, with `version` set to the confirmed version (for example `v0.18.0`) and `dry_run` set to `true`. Leave `version` empty to take the next patch.
   - GitHub MCP: `actions_run_trigger` with `method: run_workflow`, `workflow_id: release-tag.yml`, `ref: main`, `inputs: {"version": "vX.Y.Z", "dry_run": "true"}`.
   - CLI: `gh workflow run release-tag.yml --ref main -f version=vX.Y.Z -f dry_run=true`.
2. Check the run succeeded and printed the expected version and commit.
3. Dispatch it again with `dry_run` set to `false`. The workflow refuses a version that is not strictly newer than the latest tag, creates the tag on main HEAD, and creates the GitHub release with generated notes.
4. Edit the release notes on the release page if the generated notes need the categories above.

### 7. Verify

- Check the release appears at https://github.com/luthersystems/svc/releases
- The Go module proxy picks up the tag automatically. Verify at:
  `https://pkg.go.dev/github.com/luthersystems/svc@vX.Y.Z`

## Key Reminders

- Tags are on main only — never tag a feature branch
- Always ask the user to confirm the version number before tagging
- Downstream consumers update by bumping the version in their `go.mod`
- SNAPSHOT tags (e.g., `v0.14.4-SNAPSHOT.1`) are used for pre-release testing
- NEVER auto-merge pending PRs as part of a release

## Checklist

- [ ] On main branch, up to date
- [ ] `verify` skill passed
- [ ] Version number confirmed with user
- [ ] Dry run of the `Release (tag)` workflow passed
- [ ] Real run created the tag and release
- [ ] GitHub release created with release notes
