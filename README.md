# AgentPilot

Pilotage mobile/web d'agents CLI (Copilot CLI, Antigravity CLI, Mistral CLI) installés sur les machines de l'homelab. Backend Go conteneurisé servant directement l'interface web React (`web/`), runner Go natif multiplateforme, accès LAN/Tailscale uniquement.

Structure du dépôt :

- `server/` : backend Go (`cmd/server`) servant l'API, les WebSockets et les assets statiques de la SPA React (`AGENTPILOT_WEB_DIR`), ainsi que le runner natif (`cmd/runner`).
- `web/` : interface web React responsive (PWA / mobile-first) compilée dans l'image Docker et servie sur `/` par `agentpilot-server`.
- `deploy/` : `Dockerfile` multi-stage (Node + Go -> distroless), `docker-compose.yml` et documentation de déploiement (`deploy/README.md`).
- `docs/` : PRD, ADR, backlog et manifestes JSON d'agents (`docs/AgentManifests/`).
