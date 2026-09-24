# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

NotifyNYC is a Go service that polls the NYC NotifyNYC RSS feed (via Everbridge) every 5 minutes and forwards new English-language alerts to a Matrix room. It tracks already-sent messages via a log file (`/log/log.txt`) to avoid duplicates.

## Build and run

```bash
go build -o notifynyc
MATRIX_URL=https://chat.as215855.net MATRIX_TOKEN=<token> MATRIX_ROOM=<room_id> ./notifynyc
```

For local testing, override the log path:

```bash
LOG_FILE=./log.txt ./notifynyc
```

## Required environment variables

- `MATRIX_URL` — Matrix homeserver base URL (e.g. `https://chat.as215855.net`)
- `MATRIX_TOKEN` — Matrix access token for the bot account (`@notifynyc-bot:as215855.net`)
- `MATRIX_ROOM` — Matrix room ID to send messages to
- `SLACK_WEBHOOK_URL` — (optional) Slack incoming webhook for `#notifynyc` (`C0C451QLJLW`); when set, Slack delivery is authoritative and Matrix is secondary
- `LOG_FILE` — (optional) override the log file path, defaults to `/log/log.txt`

The Slack webhook is stored in OpenBao at `secret/notifynyc`, field `slack_webhook_url`, and is rendered as `SLACK_WEBHOOK_URL` in `/etc/notifynyc/env` on the service host. The value must never be committed or logged.

## Deployment

Runs as a systemd service on Proxmox LXC container 142 (`notifynyc`). To redeploy:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o notifynyc-linux
scp notifynyc-linux root@192.168.0.21:/tmp/notifynyc
ssh root@192.168.0.21 "pct push 142 /tmp/notifynyc /usr/local/bin/notifynyc --perms 755 && pct exec 142 -- systemctl restart notifynyc"
```

The service unit is `/etc/systemd/system/notifynyc.service`; its optional Slack configuration is loaded from `/etc/notifynyc/env`.

## Architecture

Single-file Go binary (`main.go`) with no external dependencies. Uses `encoding/xml` for RSS parsing and `net/http` to call the Matrix client-server API directly.

## Key behaviors

- Language filter: only items with `[English]` in the `<author>` field are forwarded (the Everbridge feed includes all languages).
- Deduplication: each entry's `PubDate` is checked against the log file.
- Message truncation: everything after "To view this message" is stripped from the RSS description.
