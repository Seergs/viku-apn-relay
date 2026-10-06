# AGENTS.md

Guidance for AI coding agents working in this repository.

## What this is

`viku-apn-relay` receives Vikunja webhook deliveries and forwards them to iOS devices
through Apple Push Notification service (APNs). It is hosted by the Viku team. Users cannot
self-host it, because APNs credentials belong to the team.

The client side lives in the `vikunja-ios` repo.

## Rules

- **Never store payloads.** Task titles, project names, and comment text are forwarded and
  discarded. Do not persist them, cache them, or write them to logs.
- **Logs carry counts and status codes only.** No task content, no tokens, no secrets.
- **Verify every webhook.** Check `X-Vikunja-Signature` (HMAC-SHA256 over the raw body)
  with a constant-time comparison before doing anything else with the request.
- **Secrets come from the environment or a secret store,** never from the repo, tests,
  fixtures, or docs. APNs credentials and per-device webhook secrets are both secrets.
- **Drop self-caused events.** Project-level events where the actor is the registered user
  are never pushed. User-level events (`task.overdue`, `task.reminder.fired`) are not dropped
  by this rule.
- **Unregister on APNs 410.** Delete the device registration when APNs reports it unregistered.

## Testing

- Use real recorded Vikunja webhook fixtures, not hand-written minimal payloads.
- Every supported event type needs a mapping test.
- Run the full test suite before every commit: `go test ./...`.

## Git

- Never commit directly to `main`. `lefthook` blocks it. Create a branch first.
- Branch names: `<type>/<short-description>`, kebab-case, no ticket id or username prefix.
- Commits: Conventional Commits, one line, imperative, lowercase, no trailing period,
  e.g. `feat(webhook): verify vikunja signature`.
- Atomic commits: one logical change per commit.
- No `Co-Authored-By` or other trailers in commit messages.
- No em dashes in code, comments, docs, or commit messages.

## Language

English only in code, comments, docs, strings, and test fixtures. This is an open-source project.
