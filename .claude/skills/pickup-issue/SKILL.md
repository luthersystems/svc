---
name: pickup-issue
description: "Pick up a GitHub issue and implement it end-to-end. Reads the issue, creates a branch, implements the fix/feature, verifies, and creates a PR. Examples: 'pickup issue #67', 'work on issue 55', 'grab that issue'."
---

# Pickup Issue

Full lifecycle: read issue → branch → implement → verify → PR.

## Workflow

### 1. Read the Issue

```bash
gh issue view <number> --json title,body,labels,assignees
```

Understand the requirements fully before writing code. Check for:
- Acceptance criteria
- Referenced files or packages
- Related issues or PRs

### 2. Assign Yourself

```bash
gh issue edit <number> --add-assignee @me
```

### 3. Create a Branch

```bash
git checkout main && git pull
git checkout -b sam-at-luther/<Issue_title_abbreviated>
```

Use the issue title (abbreviated) as the branch description.

### 4. Implement

Follow the `implement` skill:
- Read existing code first
- Write tests that capture expected behavior
- Make the change
- Format, lint, build, test

### 5. Verify

Run the `verify` skill. All CI checks must pass locally.

### 6. Create PR

Follow the `pr` skill. In the PR body, reference the issue:

```bash
gh pr create --title "Fix/Add <description>" --body "$(cat <<'EOF'
## Summary
<description of changes>

Closes #<issue-number>

## Test plan
- [ ] Tests pass locally
- [ ] Lint passes

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Using `Closes #<number>` will auto-close the issue when the PR is merged.

## Key Reminders

- Read the full issue before starting — understand the "why" not just the "what"
- Check if there are related issues or PRs that provide context
- One issue per PR — don't bundle unrelated changes

## Checklist

- [ ] Issue read and understood
- [ ] Branch created from latest main
- [ ] Implementation complete with tests
- [ ] `verify` skill passed
- [ ] PR created referencing the issue
