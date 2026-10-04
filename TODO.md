# TODO

## Planned
- Discord: threads and forums (listing then sending), voice channels and categories, resolving a nickname (`/query @name`, `/join`), `/whois`, contact book, read receipts, stickers, location cards.
- `/set lang`: the “seen <date>” presence keeps the previous language until the next event (accepted ceiling).
- Telegram: a media kept in the cache whose file reference expired downloads again only after its message is reloaded (on FILE_REFERENCE_EXPIRED, reload the message once).
- Telegram: "delete the chat" on a basic group could also leave it, as the official apps do.
- IRC: retry a first connection that fails, and back off between reconnections.
- Drawing: send only the rows that changed since the last frame (frames are already atomic and unchanged ones skipped).
