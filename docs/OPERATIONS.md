# Operations

Maintainer reference for running, configuring, and deploying the relay. If you're just
looking for what this project does, see the [README](../README.md).

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Listen address. |
| `DATABASE_PATH` | `relay.db` | SQLite database file. |
| `PUBLIC_BASE_URL` | `https://relay.viku.dev` | Origin used to build webhook URLs returned to devices. |
| `APNS_KEY_ID` | | Key id of the APNs auth key (`.p8`). Required. |
| `APNS_TEAM_ID` | | Apple team id. Required. |
| `APNS_TOPIC` | | App bundle id used as the APNs topic. Required. |
| `APNS_PRIVATE_KEY` | | Contents of the `.p8` key, PEM. Required. Keep it in a secret store, never in the repo. |
| `APNS_ENDPOINT` | `https://api.push.apple.com` | Use `https://api.sandbox.push.apple.com` for development builds. |

The relay refuses to start without the APNs settings.

## API

### `POST /v1/registrations`

Registers a device. The app calls this on launch when notifications are enabled.

Request:

```json
{
  "apns_token": "<APNs device token>",
  "webhook_secret": "<HMAC secret generated on the device, 32 to 256 bytes>",
  "vikunja_user_id": 42,
  "account_key": "<opaque id for the app's saved connection, 1 to 128 characters>"
}
```

Response `200`:

```json
{
  "id": "<opaque registration id>",
  "webhook_url": "https://relay.viku.dev/h/<opaque registration id>",
  "management_token": "<keep in the Keychain; shown only in this response>"
}
```

Idempotent per `account_key`, not per `apns_token`: one device can register several
accounts (several `account_key`s), each getting its own row, `id`, and `webhook_url`, so
enabling notifications on two accounts on the same device never collides. Registering the
same `account_key` again (including with a new `apns_token`, e.g. after a device token
rotation) keeps the same `id` and `webhook_url`, replaces the stored `apns_token`/secret/user
id, and issues a new `management_token`. The previous management token stops working. The
app must then recreate its Vikunja webhooks with the new secret.

The relay never interprets `account_key`, it only stores it and matches registrations back
by it. The app is free to use its own saved-connection id, as long as it is stable across
calls for the same connection and distinct across different connections.

`vikunja_user_id` is used only to drop self-caused project events. The relay does not verify
it against Vikunja.

### `DELETE /v1/registrations/{id}`

Unregisters the device and removes its row. Requires `Authorization: Bearer <management_token>`.

Response `204`. A repeated call returns `401`, because the row and its token no longer exist.
The end state is the same.

### Error responses

| Status | Meaning |
|---|---|
| `400` | Invalid JSON or invalid field values. |
| `401` | Missing or wrong management token, or unknown id. Both cases look the same. |
| `413` | Request body over 4 KiB. |
| `429` | Too many registration requests from this client IP. `POST /v1/registrations` allows 5 per minute, burst 5. Trusts `CF-Connecting-IP`, falling back to `X-Forwarded-For`. |
| `500` | Internal error. No detail is returned. |

### `POST /h/{id}`

Receives Vikunja webhook deliveries. Verifies `X-Vikunja-Signature` before doing anything else,
then sends the matching push through APNs. Unknown ids and bad signatures both return `401`.
A failed push returns `502` so the caller can retry. A device APNs reports as unregistered
is removed from the store and returns `200`, because a retry cannot succeed.

Alongside the `aps` alert, the push JSON carries `task_id`, `project_id` (both omitted when the
event has none) and `account_key` (the registration's own, passed through unchanged), so the app
can resolve and open the right task on tap without guessing which saved connection it belongs to.

#### Delivery deduplication

A user-directed event (`task.overdue`, `task.reminder.fired`, `tasks.overdue`) can legitimately
match both a project-level and a user-level webhook for the same registration: this happens when
the event's target user is also a project member who separately turned on project-level
notifications for that project. Vikunja's `WebhookListener.Handle` has no cross-webhook
deduplication of its own, it dispatches one HTTP delivery per matching webhook row, so the relay
receives the exact same reminder twice at the same `/h/{id}` URL.

`internal/dedup.Cache` (wired into `push.Dispatcher`, constructed once in `cmd/relay/main.go` with
a 30s window) collapses this, but only for the three events above (`notify.IsUserDirected`).
Every other event's `task_id` does not uniquely identify one occurrence on its own (several
different comments on the same task share a `task_id`, and project-level events with no task share
`task_id` 0 regardless of which project they are about), so deduping them would risk dropping a
second, genuinely different event rather than a true duplicate.

For an eligible event, `Dispatch` claims a key built from `registration id + event name + task id +
the event's target user id` (parsed from the delivery's own `data.user` field, not the
registration's stored `vikunja_user_id`) before attempting the send, and skips the send if that key
is already claimed. The claim is released as sent only once APNs actually accepts the push: a
failed send clears the claim immediately, so a delivery Vikunja retries after a failure is not
mistaken for a duplicate and silently dropped. Two different users' reminders on the same task
(for example a project-level webhook delivering a project-mate's reminder, not your own) are
**not** deduplicated against each other, because the target user id differs.

This is deliberately different from the existing `apns-collapse-id` header (see `push.Payload`):
collapse-id only replaces a still-queued, undelivered notification at APNs and does not carry a
user id, so it cannot distinguish "my reminder, sent twice" from "two different people's reminders
on the same task," and does nothing once the first notification has already been delivered.

**Single-instance assumption.** The cache is an in-memory `map` guarded by a mutex, with no shared
backing store, this works only because the relay runs as one process per stack (see
[Deployment](#deployment)). If this ever runs as multiple instances behind a load balancer, each
instance would hold its own cache, and two deliveries for the same event could land on different
instances and both get pushed: the dedup would silently stop working. Scaling out would need this
moved to shared state first: either a dedicated store (e.g. Redis, `SETNX` with a TTL) or a table
in the existing SQLite database with a `UNIQUE` constraint on the key, so a second insert for the
same key fails instead of a second push going out.

### `GET /privacy`

Serves the relay's privacy statement as a static HTML page, embedded in the binary
(`internal/privacy`). Link it from the app's consent modal.

## Storage

Only registration data is stored. Payloads, task titles, project names and comment text are
never written to the database or to logs.

Table `registrations`:

| Column | Type | Notes |
|---|---|---|
| `id` | `TEXT` primary key | Random 16 bytes, hex encoded. Appears in the webhook URL. |
| `apns_token` | `TEXT` | APNs device token. Not unique: several accounts on the same device share one. |
| `account_key` | `TEXT` unique | Opaque id for the app's saved connection. One row per account_key. |
| `webhook_secret` | `BLOB` | HMAC key used to verify deliveries. Needed in plain text, so it cannot be hashed. |
| `vikunja_user_id` | `INTEGER` | Used to drop self-caused project events. |
| `management_token_hash` | `TEXT` | SHA-256 hex of the management token. The token itself is never stored. |
| `created_at` | `INTEGER` | Unix seconds. |
| `updated_at` | `INTEGER` | Unix seconds. |

The database uses the default SQLite journal and a single connection, so writes are
serialized.

## Development

```sh
go test ./...
```

Run the relay locally:

```sh
DATABASE_PATH=./relay.db ADDR=:8080 go run ./cmd/relay
```

`*.db` files are git-ignored.

## Deployment

The relay runs as two Docker Compose stacks (`stg` and `prd`), each behind a reverse proxy that
terminates TLS for its own domain. Each stack publishes its port on `127.0.0.1` only, on a
different host port, so they don't collide on the same host.

Images are built by `.github/workflows/image.yml` and pushed to `ghcr.io/seergs/viku-apn-relay`.
Tags: the commit SHA (`sha-<full-sha>`), the branch name, `latest` for `main`, and the semver for
`v*` tags.

`deploy/compose.yaml` is shared by both stacks. What differs between stg and prd (`RELAY_TAG`,
`STACK_NAME`, `PUBLIC_BASE_URL`, `HOST_PORT`, `SECRETS_DIR`) is supplied as interpolation variables
by `.doco-cd.yml` (stg) and `.doco-cd.prd.yml` (prd) at the repo root. A
[doco-cd](https://doco.cd) daemon on the server, one instance per stack, watches its own config
file and redeploys `deploy/compose.yaml` whenever that file's `RELAY_TAG` changes.

doco-cd checks out each deployed revision into its own directory (its own artifact storage,
configured when doco-cd itself is set up), separate from wherever `.env` and the APNs key already
live on the server. So `SECRETS_DIR` in each `.doco-cd*.yml` just points at the existing per-stack
secrets directory on the server instead of a new location: nothing needs to move.

### First deploy (per stack)

The APNs key and `.env` already sit at `SECRETS_DIR/secrets/apns_key.p8` and `SECRETS_DIR/.env`
for each stack; this repo never touches them. All that's new:

1. Point the doco-cd daemon for that stack at this repo, with `working_dir: deploy` and, for prd,
   `target: prd` so it reads `.doco-cd.prd.yml` instead of `.doco-cd.yml`.

2. Check it: `curl -s <public URL>/healthz` should return `200` for each stack's domain.

The reverse proxy in front of the host already routes both domains to their respective
`127.0.0.1` ports; that config lives on the server, not in this repo.

### Upgrade / rollback

The tag to run is tracked in Git, not on the server. Open a PR that bumps `RELAY_TAG`:

- in `.doco-cd.yml` to push a build to stg,
- in `.doco-cd.prd.yml` to promote a tag already validated in stg to prd.

Once that PR merges, the matching doco-cd daemon picks up the change and redeploys. To roll back,
open the same kind of PR with the previous SHA.

Without doco-cd running (e.g. before it's set up on a stack), the same upgrade can be done by hand
from a checkout of this repo, with `SECRETS_DIR` pointed at that stack's existing directory on the
server:

```sh
cd deploy
RELAY_TAG=<sha> SECRETS_DIR=<path to that stack's secrets dir> docker compose pull
RELAY_TAG=<sha> SECRETS_DIR=<path to that stack's secrets dir> docker compose up -d
```

### Data

The SQLite database lives in the named volume `relay-data`, mounted at `/data`. It holds
registrations only. Back it up with `sqlite3 /data/relay.db ".backup"` from a copy of the volume,
and encrypt the backup before it leaves the server.
