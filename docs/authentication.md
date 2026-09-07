# Connect your accounts

[Français](authentication.fr.md) · [Back to README](../README.md) · [Full manual](guide.md)

TTYloom can connect Telegram, Discord, or both. Run it once to create
`~/.config/ttyloom/config.toml`, then edit that file. `TTYLOOM_DIR` can select
another configuration directory. Keep top-level settings before TOML sections.

## Telegram

### Your personal account: API ID and API hash

A Telegram user account uses **application credentials plus a login session**,
not a BotFather token.

1. Create and sign in to your account in an official Telegram app.
2. Open [my.telegram.org](https://my.telegram.org/) and sign in with that account.
3. Open **API development tools**, complete the application form, then copy
   your **api_id** and **api_hash**. See [Telegram’s application instructions](https://core.telegram.org/api/obtaining_api_id).
4. Edit the existing top-level keys in `config.toml`:

   ```toml
   api_id = 123456                    # your own numeric application ID
   api_hash = "YOUR_TELEGRAM_API_HASH"
   bot_token = ""                    # leave empty for a personal account
   ```

5. Start `./ttyloom`. On your phone, open **Settings → Devices → Link Desktop
   Device** and scan the QR code. Alternatively, press Enter in TTYloom for the
   phone-number flow; enter the phone number with its country code and the
   login code that Telegram sends. Complete the 2FA password prompt if enabled.

The exact names of the phone menus depend on the app language and version.
TTYloom writes the resulting session to `session.json` in its configuration
directory. That file grants account access: keep it private and out of Git.
There is no need to extract a Telegram token from your browser or phone.

`TG_API_ID` and `TG_API_HASH` can override file settings. TTYloom does not copy
environment overrides back into the file. Avoid putting secrets in shell
history, shared launch scripts or bug reports.

### A Telegram bot: get a BotFather token

1. Open the official [@BotFather](https://t.me/BotFather) conversation in Telegram.
2. Send `/newbot`, then choose the requested display name and bot username.
3. Save the returned token privately. This procedure is described in
   [Telegram’s bot tutorial](https://core.telegram.org/bots/tutorial#obtain-your-bot-token).
4. Keep your own `api_id` and `api_hash` configured: TTYloom connects through
   MTProto, so bot mode still needs those application credentials. Add:

   ```toml
   bot_token = "YOUR_BOTFATHER_TOKEN"
   ```

   You can use `TG_BOT_TOKEN` instead of storing it in the file.

5. Start TTYloom and send a message to the bot from another account. The bot
   only sees chats and messages Telegram allows it to receive.

The bot session uses `session-bot.json`, separate from your user session.
Bot mode has no dialog list, account history or account-wide search. A bot
cannot start an unsolicited private conversation with an arbitrary user.

### Lost access or exposed credentials

For a personal account, revoke the relevant session from Telegram’s **Devices**
settings, stop TTYloom and move its session file aside before logging in again.
For a bot, use BotFather to revoke and replace the token. Do not upload a session
file, login code, API hash or token when reporting a problem.

## Discord

### Know which token the current adapter accepts

The current `protocols/dsc` adapter uses a **normal user-account token** through
ningen. It does **not** implement Discord bot accounts or an OAuth2 sign-in flow.
A Developer Portal bot token, client secret or OAuth2 bearer token is not a
replacement for this user token.

Discord says that using a user token in another application can result in
suspension or termination. This is an unofficial integration, not an approved
Discord client. See [Discord’s account safety guidance](https://discord.com/safety/360044104071-Tips-against-spam-and-hacking)
and [self-bot policy](https://support.discord.com/hc/en-us/articles/115002192352-Automated-User-Accounts-Self-Bots).

### Log in with a QR code (recommended)

Write an empty `[discord]` section at the end of `config.toml` and start
TTYloom. It shows a QR code in window 0; scan it from the Discord app
(**Settings → Scan QR Code**) and confirm on the phone. This is the remote
authentication of the official desktop client: TTYloom becomes a device of
its own, listed in **Settings → Devices**, and the token it receives is saved
in `~/.config/ttyloom/discord.token` with mode `0600`. Nothing is read from a
browser or from the desktop client. `/discord logout` ends that session on the
server and deletes the file; `/discord login` shows the QR again.

### Obtain your own user token manually

Only do this in **your own signed-in browser session**. TTYloom does not read
browser profiles, recover credentials or ask for your Discord password.

1. Open [Discord’s web app](https://discord.com/app) and sign in normally.
2. Open your browser’s developer tools (**F12**, or **Ctrl+Shift+I**), then
   select **Network**. Select **Fetch/XHR** if that filter is available.
3. Reload the Discord page, or open a channel to create normal API requests.
4. Select a request sent to `https://discord.com/api/…`, such as a request for
   your channels or messages. Open **Headers → Request Headers**.
5. Find **Authorization**. Its value on an authenticated user request is the
   credential used by this adapter. Copy only the value into your password
   manager. Do not use “Copy as cURL”, export a HAR, or paste it into a ticket:
   those actions can expose the token along with other account data.
6. Close developer tools and clear the clipboard after saving it.

This is manual inspection of the web client’s current request headers, not an
official Discord token-issuance procedure. Browser labels and Discord requests
can change. If you cannot find the header, do not install a token-extractor
extension or paste scripts into the developer console.

### Store it and connect

With an already configured [pass](https://www.passwordstore.org/) store:

```bash
pass insert discord/token
```

Paste the token at the hidden prompt. Keep only the token in this entry,
without quotes, a `Bot`/`Bearer` prefix, or extra descriptive lines. Add this
section at the **end** of `config.toml`:

```toml
[discord]
token_cmd = "pass show discord/token"
```

Start `./ttyloom`. For Discord alone, leave `api_id = 0`, `api_hash = ""` and
`bot_token = ""`, and unset any `TG_*` credential overrides.

You can use another password manager or a local executable that prints only
the token. The command runs **without a shell**, splits arguments on whitespace,
and stops after 30 seconds. Pipes, redirects, shell variables and shell quoting
are not interpreted. Use an executable wrapper if its path or arguments contain
spaces. TTYloom trims surrounding whitespace and does not save the returned
token to `config.toml` or intentionally log its value.

An authentication failure normally means a revoked token, an incorrect manager
entry or a command that failed. Check the manager privately, sign in again in
the official client, and replace the saved token if needed; `/discord login`
then runs the command again without leaving TTYloom. If exposed, secure the
account using Discord’s account settings and replace the credential.

### If you meant a Discord bot token

For a separate bot project, create an application in the
[Discord Developer Portal](https://discord.com/developers/applications), open
**Bot**, and use **Reset Token**. Store it privately. See the
[official bot setup guide](https://docs.discord.com/developers/quick-start/getting-started).
**TTYloom’s current Discord adapter cannot use that token.** Bot integration
would require an implementation using the supported bot APIs.
