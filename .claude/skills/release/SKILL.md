---
name: release
description: "Create a new release tag. Determines the next version, tags main, and pushes. Examples: 'create a release', 'tag a new version', 'release v0.14.17'."
---

# Release

Tag a new semver release on main. This repo uses lightweight tags with no release automation — tagging is the release.

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
- **Patch bump** (v0.14.14 → v0.14.15): Bug fixes, dependency bumps, small improvements
- **Minor bump** (v0.14.x → v0.15.0): New packages, breaking API changes, significant features

Most releases are patch bumps.

### 5. Create and Push Tag

```bash
git tag v0.14.<next>
git push origin v0.14.<next>
```

### 6. Verify on pkg.go.dev

The Go module proxy will pick up the new tag automatically. Verify at:
`https://pkg.go.dev/github.com/luthersystems/svc@v0.14.<next>`

## Key Reminders

- Tags are on main only — never tag a feature branch
- No changelog file — release notes are optional
- Downstream consumers update by bumping the version in their `go.mod`
- SNAPSHOT tags (e.g., `v0.14.4-SNAPSHOT.1`) are used for pre-release testing

## Checklist

- [ ] On main branch, up to date
- [ ] All tests pass
- [ ] Tag created with correct version
- [ ] Tag pushed to origin
