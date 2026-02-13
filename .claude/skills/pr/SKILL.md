---
name: pr
description: "Ship changes by creating a pull request. Runs verification first, then pushes and creates PR with repo conventions. Examples: 'create a PR', 'open a pull request', 'ship this'."
---

# PR

Ship changes from a feature branch to main. Runs `verify` first, then creates a PR following repo conventions.

## Workflow

### 1. Run Verify

Run the `verify` skill first. Do not proceed if any checks fail.

### 2. Commit Changes

```bash
# Stage specific files (never use git add -A)
git add <changed-files>

# Commit with imperative subject line
git commit -m "$(cat <<'EOF'
Add short description of change

Optional body with context.

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"
```

**Commit message conventions:**
- Imperative mood: "Add", "Fix", "Bump", "Use", "Improve", "Remove"
- Short subject line (< 72 chars)
- NOT conventional commits (no `feat:`, `fix:` prefixes)
- Reference issue numbers in body if applicable

### 3. Push Branch

```bash
git push -u origin HEAD
```

### 4. Create PR

```bash
gh pr create --title "Short imperative description" --body "$(cat <<'EOF'
## Summary
- Bullet points describing changes

## Test plan
- [ ] Tests pass locally
- [ ] Lint passes

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

### 5. Wait for Review

Branch protection requires:
- 1 approving review
- All conversations resolved
- CI must pass (golangci-lint + tests)

Do NOT merge automatically — let the author review and merge.

## Key Reminders

- PRs target `main` only
- Keep PRs focused — one logical change per PR
- Branch naming: `username/Description` or descriptive kebab-case
- NEVER force push or use `--admin` to bypass branch protection
- NEVER auto-merge — always let the human review and merge

## Checklist

- [ ] `verify` skill passed
- [ ] Changes committed with proper message format
- [ ] Branch pushed to origin
- [ ] PR created with summary and test plan
- [ ] CI passing on PR
