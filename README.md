# viku-apn-relay

Receives Vikunja webhook deliveries and forwards them to iOS devices through Apple Push
Notification service. Hosted by the Viku team.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `ADDR` | `:8080` | Listen address. |
| `DATABASE_PATH` | `relay.db` | SQLite database file. |
| `PUBLIC_BASE_URL` | `https://relay.viku.app` | Origin used to build webhook URLs returned to devices. |
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
  "vikunja_user_id": 42
}
```

Response `200`:

```json
{
  "id": "<opaque registration id>",
  "webhook_url": "https://relay.viku.app/h/<opaque registration id>",
  "management_token": "<keep in the Keychain; shown only in this response>"
}
```

Idempotent per `apns_token`. Registering the same token again keeps the same `id` and
`webhook_url`, replaces the stored secret and user id, and issues a new `management_token`.
The previous management token stops working. The app must then recreate its Vikunja
webhooks with the new secret.

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
| `500` | Internal error. No detail is returned. |

### `POST /h/{id}`

Receives Vikunja webhook deliveries. Verifies `X-Vikunja-Signature` before doing anything else,
then sends the matching push through APNs. Unknown ids and bad signatures both return `401`.
A failed push returns `502` so the caller can retry. A device APNs reports as unregistered
is removed from the store and returns `200`, because a retry cannot succeed.

## Storage

Only registration data is stored. Payloads, task titles, project names and comment text are
never written to the database or to logs.

Table `registrations`:

| Column | Type | Notes |
|---|---|---|
| `id` | `TEXT` primary key | Random 16 bytes, hex encoded. Appears in the webhook URL. |
| `apns_token` | `TEXT` unique | APNs device token. One row per token. |
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
