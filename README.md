# AgentPilot

Remote-control the CLI coding agents installed on your homelab machines (GitHub
Copilot CLI, Antigravity CLI, Mistral CLI) from your phone or laptop, over LAN
or Tailscale. A containerized Go backend serves a mobile-first web UI; a native
cross-platform runner on each machine executes the agents in PTYs and streams
the terminal back over an authenticated WebSocket.

- **Server** (`server/cmd/server`): Go, Docker image on GHCR. Serves the REST
  API, the WebSocket hub and the built-in React web UI (`web/`).
- **Runner** (`server/cmd/runner`): Go binary (Linux/Windows, amd64/arm64)
  published on GitHub Releases. Dials out to the server; opens no inbound port.
- **Web UI** (`web/`): React SPA compiled into the Docker image.
- **Docs**: [installation guide](docs/INSTALL.md) ·
  [authentication & token model](docs/AUTHENTICATION.md) ·
  [deployment reference](deploy/README.md) · PRD/ADRs/backlog in this repo.

## Quick start

```sh
# 1. On the server host: pull the pinned image and create the token file
#    (generate secrets with: openssl rand -hex 32)
docker pull ghcr.io/agailloty/agentpilot-server:sha-76f46a1
mkdir -p ~/agentpilot
cat > ~/agentpilot/server.env <<'EOF'
AGENTPILOT_TOKENS=mobile-default:mobile:CHANGE_ME_MOBILE,runner-pc1:runner:CHANGE_ME_RUNNER
EOF
chmod 600 ~/agentpilot/server.env

# 2. Run the server, bound to your Tailscale/LAN IP (never 0.0.0.0 on the host)
SERVER_IP=$(tailscale ip -4)   # or your LAN IP
docker run -d --name agentpilot-server --restart unless-stopped \
  -p "${SERVER_IP}:8080:8080" \
  -v agentpilot_data:/data \
  --env-file ~/agentpilot/server.env \
  ghcr.io/agailloty/agentpilot-server:sha-76f46a1

# 3. On each machine that hosts CLI agents, install the runner from a release tag
curl -fsSL https://raw.githubusercontent.com/agailloty/agentpilot/main/scripts/install-runner.sh -o install-runner.sh
bash install-runner.sh v0.1.0 --server "http://${SERVER_IP}:8080" --token CHANGE_ME_RUNNER --roots /home/you/projects
# Windows: .\scripts\install-runner.ps1 -Version v0.1.0 -Server ... -Token ... -Roots ...

# 4. Start the runner (or install the systemd unit / scheduled task from docs/INSTALL.md)
set -a; . ~/.config/agentpilot/runner.env; set +a
agentpilot-runner

# 5. Open http://<SERVER_IP>:8080/ in a browser and sign in with the mobile token
```

Full step-by-step guide with verification and troubleshooting:
**[docs/INSTALL.md](docs/INSTALL.md)**.

## Security model (summary)

- LAN/Tailscale only — never expose the server to the public Internet; the host
  port is bound to loopback or a specific LAN/Tailscale IP, never `0.0.0.0`.
- Every `/api/*`, `/ws/*` and `/healthz` request requires
  `Authorization: Bearer <token>`; anything else gets `401`.
- Tokens are declared via `AGENTPILOT_TOKENS` (`name:scope:secret`), stored only
  as SHA-256 hashes in SQLite, scoped (`mobile` for the UI/API, `runner` for
  machines) and individually revocable. Details:
  [docs/AUTHENTICATION.md](docs/AUTHENTICATION.md).
- No secrets in Git or in the image; the runner opens no listening socket; the
  CLI agents' own credentials are never touched by AgentPilot.

## Repository layout

- `server/` — Go module: `cmd/server` (backend + static web UI hosting) and
  `cmd/runner` (native runner), `internal/` packages.
- `web/` — React web UI (mobile-first), built into the Docker image.
- `scripts/` — `install-runner.sh` (Linux) and `install-runner.ps1` (Windows)
  to install a runner binary from a release tag, with SHA-256 verification.
- `deploy/` — `Dockerfile` (multi-stage Node + Go -> distroless),
  `docker-compose.yml`, `.env.example`, deployment reference (`deploy/README.md`).
- `docs/` — `INSTALL.md`, `AUTHENTICATION.md`, agent JSON manifests
  (`docs/AgentManifests/`).
- `ADR/`, `PRD.md`, `BACKLOG.md`, `AGENTS.md` — project decision records and
  backlog (internal, in French).

## Development

See `AGENTS.md` for the working conventions (Go 1.23+, `go vet` / tests in CI,
Flutter/Riverpod for a future native app, commit style). CI
(`.github/workflows/ci.yml`) runs `go vet`/`go test -race`/lint, builds the
web UI, publishes the Docker image to GHCR (`latest`, semver and `sha-<commit>`
tags) and the runner binaries to GitHub Releases on `v*` tags (GoReleaser).
