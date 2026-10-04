# TODO

## Planned
- Discord: threads and forums (listing then sending), voice channels and categories, resolving a nickname (`/query @name`, `/join`), `/whois`, contact book, read receipts, stickers, location cards.
- `/set lang`: the “seen <date>” presence keeps the previous language until the next event (accepted ceiling).
- Telegram: a media kept in the cache whose file reference expired downloads again only after its message is reloaded (on FILE_REFERENCE_EXPIRED, reload the message once).
- Telegram: "delete the chat" on a basic group could also leave it, as the official apps do.
- IRC: retry a first connection that fails, and back off between reconnections.
- Drawing: send only the rows that changed since the last frame (frames are already atomic and unchanged ones skipped).
- WhatsApp: whatsmeow needs a SQL store (pure-Go SQLite, or a store of our own) — to decide before it starts.
- IRC: a taken nick prints its 433 line again each time the client tries to take it back (every few minutes).
- IRC: the modes of a WHOIS and the setter of a topic (333) are not shown.
- `/discord` and `/telegram` say "disconnected, reconnecting…" before the first connection, the Discord QR included.
- A blank `token_cmd` written by hand keeps the token of the QR login from being saved.
- Hooks: a local clock running fast skips every hook (the age of a message is the server date against the local clock).
