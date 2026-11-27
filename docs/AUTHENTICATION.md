# AgentPilot Authentication & Authorization

This document explains exactly how authentication works in AgentPilot:

- what a token is, how it is created, stored, checked and revoked;
- how every HTTP/WebSocket request is authenticated;
- how the server authenticates a runner machine (and why the runner never
  needs an open port).

Design decision record: `ADR/0004-authentification.md` (network perimeter =
LAN/Tailscale + application-layer Bearer tokens).

## 1. Model overview

AgentPilot is single-user and deployed on a private network (LAN or a
Tailscale tailnet). Defense in depth:

| Layer | Mechanism |
| --- | --- |
| Network | Server reachable only on LAN/Tailscale; host port bound to loopback or a specific LAN/Tailscale IP, never `0.0.0.0`, never published to the Internet. Tailscale provides WireGuard encryption and network identity. |
| Application | Every `/api/*`, `/ws/*` and `/healthz` request requires `Authorization: Bearer <secret>`. No valid token -> `401`, no exceptions. |
| Transport (TLS) | Not terminated by the server in v1 (duplicated with the tailnet layer). On plain LAN, an internal TLS reverse proxy is a deployment choice, outside the code. |

Tokens have **scopes**:

| Scope | Used by | Allowed routes |
| --- | --- | --- |
| `mobile` | Web UI / API clients (phone, laptop) | `GET/POST/DELETE /api/*`, `GET /ws/sessions/{id}` |
| `runner` | `agentpilot-runner` on a homelab machine | `GET /ws/runner` only |

Scope separation is enforced: a `runner` token calling `/api/machines` gets
`401`, and a `mobile` token opening `/ws/runner` gets `401`.

## 2. Token format and lifecycle

### 2.1 Declaration

Tokens are declared in the server environment variable `AGENTPILOT_TOKENS` as a
comma-separated list of `name:scope:secret` entries:

```env
AGENTPILOT_TOKENS=mobile-default:mobile:<64-hex-chars>,runner-pc1:runner:<64-hex-chars>,runner-nas:runner:<64-hex-chars>
```

- `secret`: generate with `openssl rand -hex 32` (32 random bytes, 64 hex
  characters). This is the **only** value clients ever send.
- `scope`: `mobile` or `runner` (anything else is rejected at startup).
- `name`: human label, used for rotation/revocation. Upsert key is
  `(name, scope)`: re-declaring the same pair with a new secret **rotates** it.

At startup the server **bootstraps** these entries idempotently into the
SQLite database (`/data/agentpilot.db`, table `tokens`). If, after bootstrap,
zero active tokens exist, the server refuses to start
(`auth: no active tokens configured; set AGENTPILOT_TOKENS`).

### 2.2 Storage — hashes only

The database stores **only the SHA-256 hex digest** of each secret
(`lower(hex(sha256(secret)))`), plus `name`, `scope`, `created_at`,
`revoked_at`. The plaintext secret exists only in the env file / client
configuration. Server logs redact secrets (`name:scope:[REDACTED]`).

### 2.3 Verification of a request token

For each request, the middleware (`server/internal/auth`):

1. extracts the secret from `Authorization: Bearer <secret>` (or, for browser
   WebSockets that cannot set headers, from the `Sec-WebSocket-Protocol` entry
   `bearer.<secret>` — see section 3.2);
2. computes `sha256(secret)` and looks the hash up in `tokens`;
3. compares the digests with `crypto/subtle.ConstantTimeCompare`;
4. rejects the request (`401`) if the token is unknown, revoked
   (`revoked_at` set), or its scope does not match the route's required scope.

A `401` response is `{"error":"unauthorized"}` with a
`WWW-Authenticate: Bearer` header.

### 2.4 Route table

| Route | Required scope |
| --- | --- |
| `GET /healthz` | any valid token |
| `GET /ws/runner` | `runner` |
| `GET /api/machines`, `/api/machines/{name}/fs` | `mobile` |
| `GET/POST /api/projects`, `DELETE /api/projects/{id}`, `POST /api/projects/{id}/exec` | `mobile` |
| `GET /api/agents`, `/api/agents/{id}/commands` | `mobile` |
| `GET /api/config/notifications` | `mobile` |
| `POST/GET/DELETE /api/sessions...` (all session routes) | `mobile` |
| `GET /ws/sessions/{id}` | `mobile` |
| `GET /api/tokens`, `POST /api/tokens`, `DELETE /api/tokens/{id}` | `mobile` |
| any other `GET`/`HEAD` (static web UI assets, only when `AGENTPILOT_WEB_DIR` is set) | none — this is the login page / app bundle |

### 2.5 Managing tokens at runtime (mobile scope)

```sh
SERVER=http://<server-ip>:8080
MOBILE=<mobile-secret>

# list tokens (no secrets, only hashes are stored server-side)
curl -s -H "Authorization: Bearer $MOBILE" $SERVER/api/tokens

# create a new token: the plaintext secret is returned ONCE
curl -s -X POST -H "Authorization: Bearer $MOBILE" -H 'Content-Type: application/json' \
  -d '{"name":"runner-nas","scope":"runner"}' $SERVER/api/tokens
# -> {"id":"...","name":"runner-nas","scope":"runner","token":"<64-hex>","createdAt":"..."}

# revoke a token (e.g. a lost machine)
curl -s -X DELETE -H "Authorization: Bearer $MOBILE" $SERVER/api/tokens/<id>
```

### 2.6 Rotation procedure

1. Generate a new secret: `openssl rand -hex 32`.
2. Replace the secret for the same `name:scope` in `AGENTPILOT_TOKENS`
   (or create the token via `POST /api/tokens` and revoke the old one).
3. Restart the server (`docker restart agentpilot-server`) — bootstrap upserts
   the new hash for that `(name, scope)`, which **immediately invalidates** the
   old secret.
4. Update the client (runner env file / web UI login) with the new secret.

Revoking without rotation: `DELETE /api/tokens/{id}` sets `revoked_at`; the
token stops working instantly, no restart needed.

## 3. How requests authenticate

### 3.1 HTTP API

```sh
curl -H "Authorization: Bearer <secret>" http://<server-ip>:8080/api/machines
```

The scheme is case-insensitive (`bearer` works), the secret is trimmed. Any
missing/malformed header -> `401`.

### 3.2 WebSockets (browsers)

Browsers cannot set custom headers on a WebSocket handshake, so the web client
passes the token in the subprotocol list:

```
Sec-WebSocket-Protocol: agentpilot.v1, bearer.<secret>
```

The server middleware falls back to this only when the request is a WebSocket
upgrade (`Upgrade: websocket`) and no `Authorization` header is present. The
first `bearer.<secret>` entry wins. Anything else -> the handshake is rejected
with `401` before the upgrade.

### 3.3 CORS

If an `Origin` header is present, the server reflects it
(`Access-Control-Allow-Origin: <origin>`) and allows the `Authorization` and
`Content-Type` headers; preflight `OPTIONS` gets `204`. CORS never bypasses
authentication — non-preflight requests still go through the auth middleware.

## 4. How the server authenticates a runner

The runner is a **client**, not a server: it opens one outbound WebSocket to
the backend and the backend drives it over that channel. There is no separate
server-to-runner credential and no inbound port on the runner machine.

### 4.1 Connection sequence

1. The runner resolves `--server http://<server-ip>:8080` to
   `ws://<server-ip>:8080/ws/runner` (`http` -> `ws`, `https` -> `wss`; the
   path is always `/ws/runner`).
2. It dials with these HTTP headers on the upgrade request:

   ```
   Authorization: Bearer <runner-secret>
   X-Machine-Name: pc1                  # --name / AGENTPILOT_MACHINE_NAME, default hostname
   X-Machine-OS: linux                  # runtime.GOOS
   X-Project-Roots: ["/srv/repos", ...] # JSON array of --roots
   ```

3. The server middleware `RequireScope("runner")` validates the Bearer secret
   exactly as in section 2.3 (SHA-256 lookup + constant-time compare + scope
   check). Failure -> `401`, the runner logs `runner websocket dial failed`
   and retries with exponential backoff (250 ms doubling up to 30 s).
4. On success the hub requires a non-empty `X-Machine-Name` (`400` otherwise),
   then upgrades the connection and registers the machine in SQLite
   (`machines` table: name, os, roots, `online=true`, `last_seen_at`). If the
   same machine name was already connected, the old connection is closed.
5. Keepalive: WebSocket ping/pong every 20 s (10 s timeout). If the connection
   drops, the runner reconnects automatically; the server marks the machine
   `online=false` on disconnect. On reconnect the runner replays its active
   session IDs and buffered output (`session-sync`, `output-chunk`) so no
   terminal output is lost.

### 4.2 Command channel (server -> runner)

Once authenticated, the server sends JSON envelopes
(`{type, sessionId, ...payload}`) over the same socket to instruct the runner:

| Direction | Message types |
| --- | --- |
| server -> runner | `session-create`, `input-write`, `session-resize`, `session-close`, `fs-mkdir`, `fs-list`, `fs-delete`, `exec-sync`, `tools-probe`, `chat-turn`, `chat-cancel`, `menu-inspect` |
| runner -> server | `output-chunk`, `session-exit`, `session-sync`, `chat-delta`, `chat-done`, `ack`, `error` |

The runner executes these **locally** — PTY spawn for agent sessions (ConPTY on
Windows, Unix PTY on Linux) and arbitrary shell commands, with the privileges
of the user running the runner. The declared `--roots` only define which
directories are scanned for projects and used as default working directories;
they are not a sandbox. Because the runner never listens on any socket, only
the holder of a `runner`-scope token can command a machine — and only through
the server. Run the runner as a dedicated, unprivileged user if you want a real
privilege boundary.

### 4.3 Mobile session channel

The web UI opens `GET /ws/sessions/{id}` with the `bearer.<mobile-secret>`
subprotocol (section 3.2). The server then bridges that socket with the
runner's connection for the session's machine and relays terminal I/O
(`replay`, `output`, `input`, `resize`, `approval-needed`, `session-exit`,
`chat-*`).

## 5. Security notes and limits

- Secrets are never written to logs, never stored in plaintext server-side,
  and never committed (env files are gitignored; use mode `0600`).
- Token lookup is by hash, comparison is constant-time.
- Revocation is per-token: rotate `runner-pc1` without touching `mobile-default`.
- No HTTPS in v1: rely on Tailscale/LAN, or put the container behind your own
  internal TLS reverse proxy (deployment decision, ADR-0004).
- The web UI keeps the mobile token in the browser `localStorage` — treat the
  browser profile as trusted; sign out to clear it.
- AgentPilot never reads, stores, proxies or modifies the CLI agents' own
  credentials (GitHub/Google/Mistral). Those stay on the runner machine.
