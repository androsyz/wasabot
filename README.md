<p align="center">
  <img src="docs/assets/mascot.svg" alt="wasabot mascot" width="160">
</p>

<h1 align="center">wasabot</h1>

A self-hosted AI assistant for WhatsApp, in a single Go binary.

Link a WhatsApp number, describe how the bot should behave in markdown, and an LLM agent answers the
messages it receives. One instance can serve many clients.

> **Status: early development.** WhatsApp pairing, message storage and a per-chat reply queue work.
> The agent answers with conversation memory and can call tools (currently `current_time`).
> The dashboard can add clients, link and unlink their WhatsApp numbers, and start or stop them.
> Invites, password reset and the other pages are designed but not wired up yet.

## Requirements

- Go 1.26+
- A WhatsApp number to link. Use a spare one: WhatsApp support is unofficial (via
  [whatsmeow](https://github.com/tulir/whatsmeow)) and carries some risk of the number being restricted.

## Quick start

```bash
cp .env.example .env    # optional, defaults work
make run
```

Set `WASABOT_LLM_API_KEY` (or `WASABOT_LLM_BASE_URL` for a local server) to enable the AI agent; without
it the bot just echoes messages. wasabot has a built-in agent; to write your own, copy
[`examples/agent.md`](examples/agent.md), edit it, and set `WASABOT_AGENT_FILE` (see [Use your own agent](docs/how-to/use-your-own-agent.md)).

On the first run wasabot creates a default account: log in with **admin** / **admin**. You are then
asked to choose your own email and a new password (12+ characters) before you can use anything else,
and admin/admin stops working.

> **Do this straight away.** Until you change it, anyone who can reach the login page can sign in with
> the default credentials. If the app is reachable by others, use one of these instead, and no default
> account is created:
>
> - `WASABOT_SETUP_CODE` (8+ characters): the first-run page asks for it before creating the admin.
> - `WASABOT_ADMIN_EMAIL` and `WASABOT_ADMIN_PASSWORD` (12+ characters): the admin is created at startup.

The web UI follows your system's light or dark setting until you switch it with the toggle in the top
right; your choice is remembered, and switching back to what your system uses goes back to following it.

The dashboard is at http://127.0.0.1:8080. To link a number, choose **Add Client**, then **Show QR** on its row and scan the
code in WhatsApp > Settings > Linked devices. Later runs reconnect without a QR, and messages that were still waiting for a reply
are answered. Send a message from another number and the bot replies.

**Stop** disconnects a client until you press **Start** or restart wasabot; **Logout** unlinks its number for good.

## Configuration

Environment variables, optionally loaded from a `.env` file. Real environment variables win.

| Variable | Default | Description |
|---|---|---|
| `WASABOT_ADDR` | `127.0.0.1:8080` | Web UI address. Put a TLS-terminating reverse proxy in front before exposing it |
| `WASABOT_DB_PATH` | `wasabot.db` | SQLite database file |
| `WASABOT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WASABOT_LOG_FORMAT` | `text` | `text` or `json` |
| `WASABOT_LLM_BASE_URL` | `https://api.openai.com/v1` | Any OpenAI-compatible API (OpenAI, OpenRouter, Ollama, ...) |
| `WASABOT_LLM_API_KEY` | empty | API key; may stay empty for local servers |
| `WASABOT_LLM_MODEL` | `gpt-4o-mini` | Default model; an agent file can override it |
| `WASABOT_AGENT_FILE` | empty (built-in agent) | Path to your own agent file: name, model, language, tools and system prompt |
| `WASABOT_FALLBACK_REPLY` | a short apology | Sent when the agent fails, so customers are not left without an answer |
| `WASABOT_RATE_LIMIT` | `10` | Messages answered per minute per chat; `0` disables |
| `WASABOT_MAX_MESSAGE_AGE` | `10m` | Older incoming messages are stored but not answered; `0` disables |
| `WASABOT_COOKIE_SECURE` | `false` | Mark cookies `Secure` when TLS ends at a reverse proxy (direct TLS is detected) |
| `WASABOT_SETUP_CODE` | empty | If set, no default account is made: the first-run page asks for this code before creating the admin |
| `WASABOT_ADMIN_EMAIL`, `WASABOT_ADMIN_PASSWORD`, `WASABOT_ADMIN_NAME` | empty, empty, `Admin` | Create the admin at startup, with no default account and no setup page (email and password go together) |
| `WASABOT_UI_PREVIEW` | `false` | Serve sample data for web pages whose backend is not built yet (`/preview/invite`) |

## Development

```bash
make check                           # vet + test
make build VERSION=0.1.0             # binary in bin/
make migrate-create name=add_things  # new goose migration
make migrate-status                  # also: migrate-up, migrate-down, migrate-reset
```

## License

[MIT](LICENSE)
