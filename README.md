# wasabot

A self-hosted AI assistant for WhatsApp, in a single Go binary.

Link a WhatsApp number, describe how the bot should behave in markdown, and an LLM agent answers the
messages it receives. One instance can serve many clients.

> **Status: early development.** WhatsApp pairing, message storage and a per-chat reply queue work.
> Replies are an echo for now; the LLM agent is next.

## Requirements

- Go 1.26+
- A WhatsApp number to link. Use a spare one: WhatsApp support is unofficial (via
  [whatsmeow](https://github.com/tulir/whatsmeow)) and carries some risk of the number being restricted.

## Quick start

```bash
cp .env.example .env    # optional, defaults work
make run
```

On first run a QR code is shown. Scan it in WhatsApp > Settings > Linked devices. Later runs reconnect
without a QR. Send a message from another number and the bot replies.

## Configuration

Environment variables, optionally loaded from a `.env` file. Real environment variables win.

| Variable | Default | Description |
|---|---|---|
| `WASABOT_ADDR` | `:8080` | HTTP listen address (reserved for the web UI) |
| `WASABOT_DB_PATH` | `wasabot.db` | SQLite database file |
| `WASABOT_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `WASABOT_LOG_FORMAT` | `text` | `text` or `json` |
| `WASABOT_LLM_BASE_URL` | `https://api.openai.com/v1` | Any OpenAI-compatible API (OpenAI, OpenRouter, Ollama, ...) |
| `WASABOT_LLM_API_KEY` | empty | API key; may stay empty for local servers |
| `WASABOT_LLM_MODEL` | `gpt-4o-mini` | Default model; an agent file can override it |

## Development

```bash
make check                           # vet + test
make build VERSION=0.1.0             # binary in bin/
make migrate-create name=add_things  # new goose migration
make migrate-status                  # also: migrate-up, migrate-down, migrate-reset
```

## License

[MIT](LICENSE)
