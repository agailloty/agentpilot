# AgentPilot Deployment

Deploy `agentpilot-server` on an homelab host reachable only over LAN or a Tailscale tailnet.

## Security Rails

- **No public exposure**: Never expose `agentpilot-server` via a public reverse proxy, port forwarding, or public tunnel.
- **Explicit host bind**: `docker-compose.yml` binds `${AGENTPILOT_BIND_IP:-127.0.0.1}:${AGENTPILOT_PORT:-8080}:8080`. Keep `127.0.0.1` for local-only testing or set `AGENTPILOT_BIND_IP` to your Tailscale/LAN interface IP. Never bind `0.0.0.0` on the host.
- **Mandatory Bearer auth**: Every `/api/...`, `/ws/...`, and `/healthz` request requires `Authorization: Bearer <token>`. Unauthenticated requests always return `401`. When `AGENTPILOT_WEB_DIR` is set, only static React SPA assets (`GET`/`HEAD` outside `/api/`, `/ws/`, `/healthz`) are served publicly so the browser can load the login/app bundle.
- **No secrets in Git**: Store tokens in `deploy/.env` (gitignored) or an external secret mount.

## Quick Start

1. Create `deploy/.env` from the template:

   ```sh
   cp deploy/.env.example deploy/.env
   ```

2. Generate distinct 32-byte tokens for mobile and runner clients:

   ```sh
   openssl rand -hex 32
   ```

   Set `AGENTPILOT_TOKENS` in `deploy/.env` using `name:scope:secret` entries:

   ```env
   AGENTPILOT_TOKENS=mobile-default:mobile:<mobile-secret>,runner-pc1:runner:<runner-secret>
   ```

   Tokens are seeded idempotently into SQLite (`tokens` table, stored strictly as SHA-256 hex hashes) on server startup. If the `tokens` table has zero active tokens and `AGENTPILOT_TOKENS` is unset, the server refuses to start.

   **Token rotation**: generate a new secret with `openssl rand -hex 32`, replace the secret for the corresponding `name:scope` entry in `AGENTPILOT_TOKENS`, restart `agentpilot-server`, and update the client (`agentpilot-runner` or web UI). Updating `name:scope:<new-secret>` overwrites the stored hash for that `(name, scope)` pair and invalidates the old secret immediately.

3. Set `AGENTPILOT_BIND_IP` to your Tailscale IP (for example `tailscale ip -4`) or LAN IP.

4. Start the server:

   ```sh
   docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
   ```

   The container builds and serves the React web UI directly on `http://<AGENTPILOT_BIND_IP>:<AGENTPILOT_PORT>/` (via `AGENTPILOT_WEB_DIR=/app/web`). Open it from your phone or desktop browser over LAN/Tailscale and sign in with your `mobile` Bearer token.

## Persistent Data (`/data`)

The named volume `agentpilot_data` is mounted at `/data` (`AGENTPILOT_DATA_DIR=/data`) and stores the SQLite database and compressed session snapshots. Back up this volume to preserve session history and state.

## Notifications (ntfy)

When `AGENTPILOT_NTFY_URL` and `AGENTPILOT_NTFY_TOPIC` are set, `agentpilot-server` publishes push notifications to `<AGENTPILOT_NTFY_URL>/<AGENTPILOT_NTFY_TOPIC>` when:

- An active session matches one of its manifest `approvalPatterns` and stays idle for `AGENTPILOT_APPROVAL_IDLE_MS` (`"<Agent> attend une confirmation"`).
- An active session exits or is closed (`"Session <Agent> terminée"`).

Web clients can inspect the active notification target via `GET /api/config/notifications` (authenticated with a `mobile` Bearer token) and subscribe the Android ntfy app to that topic over LAN/Tailscale.

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `AGENTPILOT_BIND_IP` | `127.0.0.1` | Host IP published by Docker Compose (`ports`). |
| `AGENTPILOT_PORT` | `8080` | Host port published by Docker Compose (`ports`). |
| `AGENTPILOT_LISTEN_ADDR` | `127.0.0.1:8080` | Server TCP listen address (`0.0.0.0:8080` inside container only). |
| `AGENTPILOT_DATA_DIR` | `./data` | Directory for SQLite DB and session snapshots (`/data` in container). |
| `AGENTPILOT_MANIFESTS_DIR` | `../docs/AgentManifests` | Directory containing agent JSON manifests (`/app/manifests` in container). |
| `AGENTPILOT_WEB_DIR` | *(empty)* | Optional directory containing built React SPA static assets (`/app/web` in container). When empty, all routes require Bearer auth. |
| `AGENTPILOT_TOKENS` | *(required)* | Comma-separated `name:scope:secret` (`mobile` or `runner`) Bearer tokens. |
| `AGENTPILOT_NTFY_URL` | *(empty)* | Base URL of self-hosted ntfy server. |
| `AGENTPILOT_NTFY_TOPIC` | *(empty)* | ntfy topic name for push notifications. |
| `AGENTPILOT_APPROVAL_IDLE_MS` | `1500` | Idle delay in milliseconds before raising an approval alert. |
