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

```bash
git tag vX.Y.Z
git push origin vX.Y.Z

gh release create vX.Y.Z --title "vX.Y.Z" --notes "$(cat <<'EOF'
## What's Changed

### Features
- Description of feature (#PR)

### Fixes
- Description of fix (#PR)

### Dependencies
- Bump package from vA to vB (#PR)

**Full Changelog**: https://github.com/luthersystems/svc/compare/<previous-tag>...vX.Y.Z
EOF
)"
```

Use `gh release create --generate-notes` as a starting point if there are many changes, then edit for clarity.

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
- [ ] Tag created and pushed
- [ ] GitHub release created with release notes
