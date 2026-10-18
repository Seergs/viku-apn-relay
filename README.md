# viku-apn-relay

Push notifications for [Viku](https://github.com/Seergs/vikuapp), the iOS app for
[Vikunja](https://vikunja.io).

Vikunja can't send push notifications to iPhones on its own: that requires talking to Apple
Push Notification service (APNs), which needs credentials only the Viku team holds. This
relay is the piece in between. Vikunja sends it a webhook when something happens (a task is
assigned to you, a comment is added, a task is overdue...), and the relay turns that into a
push notification on your phone.

## How it works

1. You enable notifications in the Viku app and agree to the consent screen.
2. The app registers your device with the relay and sets up the matching webhooks in your
   Vikunja instance.
3. From then on, Vikunja notifies the relay, and the relay notifies your phone.

Notifications are opt-in, and you choose what triggers them: your own reminders and overdue
tasks, project activity, or both. You never get notified for changes you made yourself.

## Privacy

The relay is designed to see as little as possible:

- It never stores or logs task titles, project names, comments, or any other content from
  your tasks. Logs only ever contain counts and status codes.
- It only keeps what it needs to deliver pushes: your device token, the webhook secret used
  to verify that deliveries really come from your Vikunja instance, and your Vikunja user id
  (used to tell your own changes apart from everyone else's).
- Turning off notifications in the app removes your registration from the relay entirely.

Read the full statement at `/privacy` on the relay's URL (also linked from the app).

## Self-hosting

Because pushing to the App Store build requires Apple credentials that belong to the Viku
team, this relay can't be self-hosted for the published app. The code is public so you can
see exactly what it does with your data.

## For maintainers

Configuration, the HTTP API, database schema, and deployment are documented in
[docs/OPERATIONS.md](docs/OPERATIONS.md).
