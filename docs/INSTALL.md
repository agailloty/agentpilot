# AgentPilot Installation Guide

This guide installs a complete AgentPilot setup from scratch:

1. the **server** (Docker image from GHCR), which serves the API, the WebSocket
   hub and the built-in web UI;
2. one or more **runners** (native binaries from GitHub Releases) installed on the
   homelab machines that host your CLI agents (Copilot CLI, Antigravity CLI,
   Mistral CLI);
3. **sign-in** to the web UI from a phone or desktop browser over LAN/Tailscale.

Everything below is LAN/Tailscale-only by design. AgentPilot must never be
exposed to the public Internet (see [Security rails](#security-rails)).

- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Step 1 - Deploy the server](#step-1--deploy-the-server)
- [Step 2 - Create the authentication tokens](#step-2--create-the-authentication-tokens)
- [Step 3 - Install a runner on a homelab machine](#step-3--install-a-runner-on-a-homelab-machine)
- [Step 4 - Start the runner and keep it running](#step-4--start-the-runner-and-keep-it-running)
- [Step 5 - Sign in to the web UI](#step-5--sign-in-to-the-web-ui)
- [Step 6 - Verify the whole setup](#step-6--verify-the-whole-setup)
- [Upgrades](#upgrades)
- [Troubleshooting](#troubleshooting)
- [Security rails](#security-rails)

Auth details (token format, scopes, how the server authenticates a runner) are
in [docs/AUTHENTICATION.md](AUTHENTICATION.md). Server environment variables are
also documented in [deploy/README.md](../deploy/README.md).

## Architecture

```
 phone / laptop browser                homelab server host                 runner machine (your PC/NAS)
 ┌──────────────────┐   HTTPS-less    ┌──────────────────────────┐        ┌──────────────────────────┐
 │ web UI (React)   │  Bearer token     │ docker: agentpilot-server│        │ agentpilot-runner       │
 │ served by server │ ────────────────► │  - REST API /api/*       │        │  - dials OUT to /ws/runner│
 └──────────────────┘  LAN / Tailscale  │  - WebSocket hub         │        │  - runs agent CLIs in    │
                                        │  - SQLite in /data       │◄────── │    PTYs (Copilot, ...)  │
                                        │  - web UI static assets  │  WS    │  - no inbound port open  │
                                        └──────────────────────────┘        └──────────────────────────┘
```

Key points:

- The **runner always connects outbound** to the server (`GET /ws/runner`,
  WebSocket). The runner machine needs **no inbound port** and works behind NAT.
- The server is the only component you expose on the network, bound to a
  specific LAN/Tailscale IP (never `0.0.0.0` on the host).
- Every API/WebSocket request requires a Bearer token; see
  [docs/AUTHENTICATION.md](AUTHENTICATION.md).

### Published artifacts

| Artifact | Where | Tags / files |
| --- | --- | --- |
| Server Docker image | `ghcr.io/agailloty/agentpilot-server` | `latest`, semver (`0.1.0` from tag `v0.1.0`), `sha-<commit>` (e.g. `sha-76f46a1`) |
| Runner binaries | [GitHub Releases](https://github.com/agailloty/agentpilot/releases) | per tag `vX.Y.Z`: `agentpilot-runner_linux_amd64.tar.gz`, `agentpilot-runner_linux_arm64.tar.gz`, `agentpilot-runner_windows_amd64.zip`, `checksums.txt` |
| Web UI | bundled inside the Docker image (`/app/web`) | nothing to install separately |

Pin a version in production: prefer `sha-<commit>` or a semver tag over
`latest`. To list available image tags:

```sh
docker pull ghcr.io/agailloty/agentpilot-server:sha-76f46a1   # example of a pinned, known-good image
```

To list release tags for the runner:

```sh
git ls-remote --tags https://github.com/agailloty/agentpilot.git 'v*'
```

## Prerequisites

On the **server host** (e.g. a NAS or always-on mini PC):

- Docker Engine with the Compose plugin (`docker --version`, `docker compose version`)
- A LAN IP or a Tailscale tailnet IP for the server host (`tailscale ip -4`)
- `openssl` (to generate token secrets) or any random generator:
  `openssl rand -hex 32`

On each **runner machine** (the computers that have the CLI agents installed):

- Linux (amd64/arm64) or Windows (amd64)
- `curl` + `tar` (Linux) or PowerShell (Windows) if you use the install scripts
- Network access **to** the server host (LAN or Tailscale); no inbound port needed
- The agent CLIs (Copilot CLI, Antigravity CLI, Mistral CLI) already installed
  and authenticated on that machine. AgentPilot never touches their credentials.

## Step 1 - Deploy the server

### 1.1 Pull the image

```sh
docker pull ghcr.io/agailloty/agentpilot-server:sha-76f46a1
```

(or `:latest`, or a semver tag such as `:0.1.0` — see the table above.)

### 1.2 Create the server environment file

The only mandatory setting is `AGENTPILOT_TOKENS` (created in step 2 below).
Keep it in a file readable only by you, **never committed to Git**:

```sh
mkdir -p ~/agentpilot
cat > ~/agentpilot/server.env <<'EOF'
# name:scope:secret -- generate each secret with: openssl rand -hex 32
AGENTPILOT_TOKENS=mobile-default:mobile:REPLACE_MOBILE_SECRET,runner-pc1:runner:REPLACE_RUNNER_SECRET

# Optional: self-hosted ntfy push notifications (LAN/Tailscale only)
# AGENTPILOT_NTFY_URL=http://100.64.0.10:8090
# AGENTPILOT_NTFY_TOPIC=agentpilot
EOF
chmod 600 ~/agentpilot/server.env
```

### 1.3 Run the container

```sh
docker run -d --name agentpilot-server --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v agentpilot_data:/data \
  --env-file ~/agentpilot/server.env \
  ghcr.io/agailloty/agentpilot-server:sha-76f46a1
```

The image already sets the container-internal configuration
(`AGENTPILOT_LISTEN_ADDR=0.0.0.0:8080`, `AGENTPILOT_DATA_DIR=/data`,
`AGENTPILOT_MANIFESTS_DIR=/app/manifests`, `AGENTPILOT_WEB_DIR=/app/web`), so the
env file only needs `AGENTPILOT_TOKENS` (plus optional ntfy settings).

**To make the server reachable from other devices** (your phone, your laptop),
bind the published port to the server's Tailscale or LAN IP instead of
loopback — and never to `0.0.0.0`:

```sh
SERVER_IP=$(tailscale ip -4)   # or your LAN IP, e.g. 192.168.1.20
docker run -d --name agentpilot-server --restart unless-stopped \
  -p "${SERVER_IP}:8080:8080" \
  -v agentpilot_data:/data \
  --env-file ~/agentpilot/server.env \
  ghcr.io/agailloty/agentpilot-server:sha-76f46a1
```

> Security rail: `0.0.0.0:8080:8080` on the host would expose the server to
> every interface, including any public one. Do not do it. See
> [Security rails](#security-rails).

Alternative: build and run from source with Docker Compose
(`docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d`),
using `AGENTPILOT_BIND_IP` / `AGENTPILOT_PORT` from `deploy/.env` — see
[deploy/README.md](../deploy/README.md).

### 1.4 Check the server is up

```sh
# Without a token -> 401 (this is expected and proves auth is enforced)
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/healthz

# With the mobile token -> 200 {"status":"ok"}
curl -s -H "Authorization: Bearer REPLACE_MOBILE_SECRET" http://127.0.0.1:8080/healthz
```

Logs: `docker logs -f agentpilot-server` (look for
`agentpilot-server listening` and `active_tokens`).

## Step 2 - Create the authentication tokens

AgentPilot uses Bearer tokens declared in `AGENTPILOT_TOKENS` as a
comma-separated list of `name:scope:secret` entries:

```sh
MOBILE_SECRET=$(openssl rand -hex 32)   # 64 hex characters
RUNNER_SECRET=$(openssl rand -hex 32)

cat > ~/agentpilot/server.env <<EOF
AGENTPILOT_TOKENS=mobile-default:mobile:${MOBILE_SECRET},runner-pc1:runner:${RUNNER_SECRET}
EOF
chmod 600 ~/agentpilot/server.env
```

- `scope=mobile` tokens sign in to the web UI and call `/api/*` + `/ws/sessions/*`.
- `scope=runner` tokens let a machine register on `/ws/runner`.
- Scopes are enforced server-side: a runner token gets `401` on `/api/*` and a
  mobile token gets `401` on `/ws/runner`.
- Secrets are stored **only as SHA-256 hashes** in the SQLite database
  (`/data/agentpilot.db`); the server refuses to start if no active token
  exists. Full details: [docs/AUTHENTICATION.md](AUTHENTICATION.md).

Then (re)start the server so it picks up the tokens:

```sh
docker restart agentpilot-server   # or re-run the docker run command from 1.3
```

One runner token per machine is recommended (`runner-pc1`, `runner-nas`, ...) so
a lost or compromised machine can be revoked individually.

## Step 3 - Install a runner on a homelab machine

Pick the release tag you want to install (e.g. `v0.1.0`) and run the matching
script **on the runner machine** (not on the server).

### Linux (amd64 / arm64)

```sh
curl -fsSL https://raw.githubusercontent.com/agailloty/agentpilot/main/scripts/install-runner.sh -o install-runner.sh
bash install-runner.sh v0.1.0 \
  --server http://<server-ip>:8080 \
  --token <RUNNER_SECRET> \
  --roots /home/you/projects,/srv/repos
```

Or clone the repo and run `scripts/install-runner.sh v0.1.0 ...`.

What the script does:

1. resolves the tag (`v0.1.0`, `0.1.0` and `latest` all work),
2. downloads `checksums.txt` and `agentpilot-runner_linux_<arch>.tar.gz` from
   `https://github.com/agailloty/agentpilot/releases/download/<tag>/`,
3. verifies the archive SHA-256 against `checksums.txt` and aborts on mismatch,
4. installs the binary to `/usr/local/bin` (root) or `~/.local/bin`,
5. writes the connection settings to `/etc/agentpilot/runner.env` (root) or
   `~/.config/agentpilot/runner.env` (mode `0600`).

Useful options: `--install-dir`, `--env-file`, `--name`, `--repo`, `--force`,
`--dry-run`, `--help`.

### Windows (amd64)

In PowerShell (5.1 or 7+):

```powershell
Set-ExecutionPolicy -Scope Process Bypass
Invoke-WebRequest -Uri https://raw.githubusercontent.com/agailloty/agentpilot/main/scripts/install-runner.ps1 -OutFile install-runner.ps1
.\install-runner.ps1 -Version v0.1.0 `
  -Server http://<server-ip>:8080 `
  -Token <RUNNER_SECRET> `
  -Roots "C:\Users\you\projects"
```

The script downloads `agentpilot-runner_windows_amd64.zip`, verifies it against
`checksums.txt`, installs `agentpilot-runner.exe` to
`%LOCALAPPDATA%\agentpilot\bin`, optionally adds that folder to your user PATH
(it prints the exact command if not), and writes the connection settings to
`%LOCALAPPDATA%\agentpilot\runner.env` with an ACL restricted to your user.

### Manual installation (no script)

1. Go to <https://github.com/agailloty/agentpilot/releases/tag/v0.1.0>
2. Download the archive for the machine
   (`agentpilot-runner_linux_amd64.tar.gz`, `..._linux_arm64.tar.gz`,
   `..._windows_amd64.zip`) and `checksums.txt`
3. Verify, e.g. on Linux:
   `sha256sum -c checksums.txt --ignore-missing` (or compare manually), then
   `tar -xzf agentpilot-runner_linux_amd64.tar.gz` and move `agentpilot-runner`
   into a directory on your `PATH`.

## Step 4 - Start the runner and keep it running

### Quick start (foreground)

```sh
# using the env file written by the installer
set -a; . ~/.config/agentpilot/runner.env; set +a
agentpilot-runner

# or with explicit flags
agentpilot-runner \
  --server http://<server-ip>:8080 \
  --token <RUNNER_SECRET> \
  --name pc1 \
  --roots /home/you/projects,/srv/repos
```

Expected log line once connected: `runner connected to server`
(JSON logs on stdout; the runner reconnects automatically with exponential
backoff if the connection drops).

Runner flags and environment variables:

| Flag | Environment variable | Meaning |
| --- | --- | --- |
| `--server` | `AGENTPILOT_SERVER_URL` | Backend URL, `http(s)://` or `ws(s)://` (required) |
| `--token` | `AGENTPILOT_RUNNER_TOKEN` | Runner-scope Bearer secret (required) |
| `--name` | `AGENTPILOT_MACHINE_NAME` | Display name (default: hostname) |
| `--roots` | `AGENTPILOT_PROJECT_ROOTS` | Comma-separated directories scanned for projects |

Other commands: `agentpilot-runner --version` prints the release version;
`agentpilot-runner inspect --agent-id <id> --cmd <binary> [--args ...] [--manifest path] [--timeout 8s]`
headlessly probes an agent CLI (useful to check a CLI works under a PTY).

`--roots` matters: projects proposed in the UI are subdirectories of these
roots, and every agent session runs with its project directory as working
directory.

### Linux: systemd service

```sh
sudo install -d -m 0755 /etc/agentpilot
sudo cp ~/.config/agentpilot/runner.env /etc/agentpilot/runner.env
sudo chmod 600 /etc/agentpilot/runner.env
sudo tee /etc/systemd/system/agentpilot-runner.service <<'EOF'
[Unit]
Description=AgentPilot runner
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/agentpilot/runner.env
ExecStart=/usr/local/bin/agentpilot-runner
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable --now agentpilot-runner
sudo journalctl -u agentpilot-runner -f   # look for "runner connected to server"
```

(If you installed to `~/.local/bin`, point `ExecStart` there and use a
`--user` unit instead.)

### Windows: scheduled task at logon

Create a small launcher that loads the env file, then register it:

```powershell
$bin = "$env:LOCALAPPDATA\agentpilot\bin\agentpilot-runner.exe"
$envFile = "$env:LOCALAPPDATA\agentpilot\runner.env"
$launcher = "$env:LOCALAPPDATA\agentpilot\run-agentpilot-runner.cmd"
@"
@echo off
for /f "usebackq tokens=1,* delims==" %%a in ("$envFile") do set "%%a=%%b"
"$bin" %*
"@ | Set-Content -Path $launcher -Encoding ASCII

$action   = New-ScheduledTaskAction -Execute $launcher
$trigger  = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 10 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName 'AgentPilotRunner' -Action $action -Trigger $trigger -Settings $settings -Description 'AgentPilot runner' -Force
```

Check it in Task Scheduler (`taskschd.msc`) or with
`Get-ScheduledTaskInfo -TaskName AgentPilotRunner`.

## Step 5 - Sign in to the web UI

1. From a phone or laptop on the same LAN/Tailscale, open
   `http://<server-ip>:8080/` (e.g. `http://100.64.0.10:8080/`).
2. The login screen asks for a **Bearer token (scope mobile)** — paste the
   `mobile` secret from step 2. The base URL defaults to the address you
   opened, so leave it empty unless the server is behind a path-rewriting proxy.
3. The token is kept in the browser's `localStorage`; sign out clears it.

In the UI you should see your machine (the runner's `--name`, default hostname)
listed as online, the available agents (from the manifests bundled in the
image: Copilot, Antigravity, Mistral, shell), and your projects under the
declared roots. Create a session on a project and the agent CLI runs on the
runner machine in a PTY; the terminal stream, approvals and chat come back over
the WebSocket.

## Step 6 - Verify the whole setup

Run this checklist on any machine that can reach the server:

```sh
SERVER=http://<server-ip>:8080
MOBILE=<MOBILE_SECRET>

# 1. server up, auth enforced
curl -s -o /dev/null -w 'no token  -> %{http_code}\n' $SERVER/healthz          # expect 401
curl -s -H "Authorization: Bearer $MOBILE" $SERVER/healthz                      # expect {"status":"ok"}

# 2. wrong scope is rejected (runner token on the API -> 401)
curl -s -o /dev/null -w 'runner on /api -> %{http_code}\n' \
  -H "Authorization: Bearer <RUNNER_SECRET>" $SERVER/api/machines               # expect 401

# 3. the runner machine is registered and online
curl -s -H "Authorization: Bearer $MOBILE" $SERVER/api/machines | python3 -m json.tool

# 4. agents from the bundled manifests are listed
curl -s -H "Authorization: Bearer $MOBILE" $SERVER/api/agents | python3 -m json.tool
```

Also check `docker logs agentpilot-server` for `runner connected` /
`active_tokens`, and on the runner machine the `runner connected to server`
log line. Then open the web UI (step 5) and start a session.

## Upgrades

- **Server**: pull the new image tag (`docker pull ghcr.io/agailloty/agentpilot-server:<tag>`),
  recreate the container with the same `-v agentpilot_data:/data` volume (the
  SQLite DB and tokens persist), e.g.:
  `docker rm -f agentpilot-server && docker run -d ... ghcr.io/agailloty/agentpilot-server:<tag>`
- **Runner**: re-run the install script with the new version
  (`bash install-runner.sh v0.2.0 --force ...`) and restart the service
  (`sudo systemctl restart agentpilot-runner`). The server and runners do not
  need to be the same version, but keep them close.

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `401 {"error":"unauthorized"}` on every API call | Missing/wrong token, wrong scope, or token not in `AGENTPILOT_TOKENS` | Check the `Authorization: Bearer <secret>` header; use a `mobile`-scope secret for `/api/*`; restart the server after editing `server.env` |
| Server container exits immediately | `AGENTPILOT_TOKENS` empty/unset and no active tokens in the DB | `docker logs agentpilot-server`; set `AGENTPILOT_TOKENS` in the env file and restart |
| `curl http://<ip>:8080/healthz` returns connection refused from another device | Port bound to `127.0.0.1` only, or firewall | Re-publish with `-p <LAN-or-Tailscale-IP>:8080:8080`; check the host firewall |
| Runner logs `runner websocket dial failed` | Server URL unreachable from the runner, wrong scheme, or `401` | Check `AGENTPILOT_SERVER_URL` from the runner machine (`curl -v $URL/healthz`); the runner needs outbound access to the server, no inbound port; the token must have scope `runner` |
| Install script: `failed to download ... for tag` | Tag does not exist or asset missing | List tags: `git ls-remote --tags https://github.com/agailloty/agentpilot.git 'v*'`; use `latest` or an existing `vX.Y.Z` |
| Install script: `SHA-256 mismatch` | Corrupted download or tampered asset | Re-run the script; never install a file that fails verification |
| Machine shows offline in the UI | Runner not running / connection dropped | `systemctl status agentpilot-runner` or Task Scheduler; the runner reconnects automatically |
| `docker pull ghcr.io/...` denied | Image/tag name typo | Tags: `latest`, `0.1.0`, `sha-<commit>` (e.g. `sha-76f46a1`) |
| Web UI loads but login fails | You pasted a `runner`-scope token | Sign in with a `mobile`-scope token |

## Security rails

- **Never expose the server publicly**: no public reverse proxy, no port
  forwarding, no public tunnel. Bind the published port to loopback, a LAN IP
  or a Tailscale IP only — never `0.0.0.0` on the host.
- **Every request needs a valid Bearer token** (`/api/*`, `/ws/*`, `/healthz`);
  without it the server answers `401`. Only the static web UI assets are
  public, so the browser can load the login page.
- **No secrets in Git or images**: tokens live in `~/agentpilot/server.env`
  (mode `0600`, gitignored) or a secret mount — see `deploy/README.md`.
- **The runner opens no listening socket**: it only dials out to the server, so
  it cannot be reached directly from the network.
- **The runner executes arbitrary commands by design**: run it as a dedicated
  unprivileged user, and treat every `runner`-scope token as full remote-code
  execution on that machine. `--roots` is not a sandbox — it only defines which
  directories are scanned for projects.
- **Rotate/revoke tokens** by editing `AGENTPILOT_TOKENS` (restart required) or
  via `DELETE /api/tokens/{id}` from the web UI — see
  [docs/AUTHENTICATION.md](AUTHENTICATION.md).
- AgentPilot **never stores, proxies or manages** the credentials of the CLI
  agents (GitHub/Google/Mistral tokens stay on the runner machine).
