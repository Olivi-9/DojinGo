# DojinGo

[中文](README-zh.md) | English

Bot that downloads EH/EX/NH galleries locally and republishes them to Telegraph.

## What Changed

- The bot now talks to supported sites directly with Go's `net/http`.
- Cache is local only: in-memory or filesystem-backed.
- Optional inbound proxy servers are built into the bot process.

## Supported Sources

- `e-hentai`
- `exhentai`
- `nhentai`

`pixiv` remains reserved in the config surface, but there is no collector implementation yet in the current runtime.

## Commands

- `/start`
- `/help`
- `/sync <url>`
- `/id`
- `/version`
- `/cancel`
- `/delete <cache-key>` for admins

Messages or captions that contain a supported gallery URL are also synchronized automatically

## Quick Start

1. Copy [config_example.yaml](config_example.yaml) to `config.yaml`.
2. Fill in your Telegram bot token and at least one Telegraph token.
3. If you need `exhentai`, add the required cookies under `collectors.exhentai`.
4. Start with Docker:

```bash
docker compose up -d --build
```

Or run locally:

```bash
go run ./cmd/ehbot -config ./config.yaml
```

## Configuration

Example:

```yaml
bot:
  token: "YOUR_BOT_TOKEN"
  admins: [123456789]

telegraph:
  tokens: ["YOUR_TELEGRAPH_TOKEN"]
  author_name: "Author"
  author_url: "https://example.com"

ipv6:
  prefix: ""

storage:
  type: "memory"
  path: "./cache"
  ttl: 3888000
  max_entries: 1024

proxy:
  listen:
    http: "127.0.0.1:8080"
    socks5: "127.0.0.1:1080"
  auth:
    enabled: false
    username: ""
    password: ""
  rate_limit_per_minute: 120

collectors:
  exhentai:
    ipb_pass_hash: ""
    ipb_member_id: ""
    igneous: ""
  pixiv:
    session: ""

whitelist:
  enabled: false
  ids: [123456789]
```

Notes:

- `storage.type` supports `memory` and `file`.
- `storage.path` is only used by file storage.
- `ipv6.prefix` can be a larger IPv6 CIDR such as `2001:db8::/64` for rotating local source addresses.
- If `proxy.listen.http` and `proxy.listen.socks5` are empty, the built-in proxy servers stay disabled.

## IPv6 Rotation

If you own a routed IPv6 prefix and want per-connection source rotation:

1. Bind the prefix locally.
2. Enable `net.ipv6.ip_nonlocal_bind=1`.
3. Set `ipv6.prefix` in the config.

This is optional. Leaving `ipv6.prefix` empty uses normal local networking.

## Development

Requirements:

- Go `1.26`

Useful commands:

```bash
go test ./...
go build ./cmd/ehbot
```

## Deployment Notes

- The container reads `CONFIG_FILE`, defaulting to `config.yaml`.
- File-backed cache needs a writable directory.
- The built-in proxy server and Telegram bot run in the same process.

# Project Origin and Changes
This project is a reimplementation of [eh2telegraph](https://github.com/qini7-sese/eh2telegraph) using Go. It adjusts the proxy configuration and storage mechanisms while retaining the core functionality.
