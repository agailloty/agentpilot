# BACKLOG : AgentPilot

Découpage en jalons. Chaque item est dimensionné pour une PR autonome testable. Le jalon est terminé quand tous ses items et leurs critères d'acceptation passent.

## Jalon M0 : Socle du repository

- [x] M0.1 Structure du repo mono-repo : `server/` (module Go), `app/` (Flutter), `docs/` (PRD, ADR, backlog), `deploy/` (Dockerfile, docker-compose, exemple de config).
- [x] M0.2 Module Go : `cmd/server` (API HTTP `net/http` + WebSocket `coder/websocket`) et `cmd/runner` (worker CLI multiplateforme), packages partagés dans `internal/`, tests `go test`, configuration `golangci-lint` (avec `go vet` bloquant).
- [x] M0.3 CI GitHub Actions : build + tests Go (`go vet ./...`, `go test -race ./...`), `flutter analyze` + `flutter test`.
- [x] M0.4 Ebauche Flutter : app squelette, routing, thème sombre par défaut, page « pas de serveur configuré ».
- AC : `go build ./...`, `go test ./...` et `flutter analyze` verts en CI sur PR.

## Jalon M1 : Runner et sessions PTY (racine technique)

- [x] M1.1 `PtyManager` / `PtySession` : spawn d'un processus dans un PTY (via `go-pty` ou `gopty` : `creack/pty` sur Linux, ConPTY sur Windows), lecture du flux, écriture stdin, redimensionnement (`Cols`/`Rows`), arrêt propre à la fermeture. Test avec `cat` et `bash -i` (Linux) / `cmd.exe` ou `powershell.exe` (Windows).
- [x] M1.2 Client WebSocket sortant runner → backend : reconnexion avec backoff exponentiel, ping/pong, en-tête Bearer.
- [x] M1.3 Protocole binaire/JSON du flux : `output-chunk` (bytes ANSI), `input-write`, `session-create`, `session-close`, `fs-mkdir`, `fs-list`, `exec-sync` (one-shot avec exit code et sortie). Chaque message a un `sessionId` et un `correlationId`.
- [x] M1.4 Backend : hub de réception des runners, registry en mémoire, et un endpoint de debug `POST /debug/session` acceptant une commande shell et renvoyant le flux (test sans CLIs).
- [x] M1.5 Tests d'intégration : backend + runner en mémoire ou sur ports éphémères, session `echo` <-> flux vérifié bout en bout.
- AC : depuis une machine de l'homelab, une session `bash -i` démarre, reçoit des commandes, survit à la déconnexion/reconnexion du WebSocket runner, et le flux est capturé.

## Jalon M2 : Backend métier

- [x] M2.1 SQLite + migrations : driver pure Go `modernc.org/sqlite` (`CGO_ENABLED=0`), migrations SQL (`goose` + `embed.FS`), tables `machines`, `projects`, `sessions`, `agent_manifests`, `tokens` ; pragmas `WAL` et `busy_timeout` ; volume documenté.
- [x] M2.2 Auth : middleware Bearer obligatoire, tokens distincts mobile/runner, refus 401 sans exception, génération/rotation documentées.
- [x] M2.3 API projets : `GET /machines`, `POST /projects` (création de dossier, `git init` optionnel), `GET /projects?machine=`, `DELETE /projects/:id` (suppression optionnelle du dossier).
- [x] M2.4 API sessions : `POST /sessions` (agent × projet × machine → délégation au runner), `GET /sessions`, `DELETE /sessions/:id`, flux via un WebSocket mobile dédié (séparé de la voie runner).
- [x] M2.5 Import des manifestes d'agents (`docs/AgentManifests/*.json` chargés au démarrage) : `GET /agents`, `GET /agents/:id/commands`.
- [x] M2.6 One-shot : `POST /projects/:id/exec` (commande, timeout, sortie renvoyée), exécuté par le runner, exit code et sortie complète.
- [x] M2.7 Historique : snapshot compressé à la clôture de session, endpoint de lecture paginé.
- AC : via `curl` sur le LAN : authentifié, on crée un projet sur la machine du runner, on ouvre une session `bash` (aucun agent requis), on écrit, on ferme, l'historique est lisible.

## Jalon M3 : Mobile : piloter une session

- [x] M3.1 Config mobile : écran de connexion (base URL + token, stockage sécurisé), test de connexion.
- [x] M3.2 Ecran projets : liste par machine, création (nom + machine + dossier racine), ouverture de session (choix de l'agent).
- [x] M3.3 Vue session : émulateur terminal (`xterm` package) connecté au flux, zone de saisie en bas, barre d'actions (fermer, envoyer Ctrl+C, raccourcis d'approbation y/n).
- [x] M3.4 Reprise de session : rattrapage du buffer au reconnect (le backend renvoie le ring buffer à la reconnexion du WebSocket mobile).
- [x] M3.5 UX mobile : police mono réglable, redimensionnement, autoscroll intelligent (pause au doigt posé), mode sombre.
- AC : sur un téléphone Android via LAN : créer un dossier projet, ouvrir une session `bash` puis une session Copilot CLI, taper un prompt, voir le TUI complet, envoyer y/n aux approbations via le raccourci, fermer la session.

## Jalon M4 : Découverte des capacités et confort d'agent

- [x] M4.1 Manifestes complets initiaux : `copilot.json`, `antigravity.json`, `mistral.json` (commandes launch + slash commands issues des documentations).
- [x] M4.2 Palette slash commands dans la saisie mobile : autocomplétion avec description, insertion en un tap.
- [x] M4.3 Détection d'approbations côté backend (heuristique : `approvalPatterns` du manifeste + inactivité), notification locale « Copilot attend une confirmation ».
- [x] M4.4 Notifications push via ntfy auto-hébergé : publication backend → topic abonné par l'app, notification sur approbation détectée et sur fin de session.
- [x] M4.5 Ecran one-shot : formulaire commande + projet, affichage de la sortie, historique des relances.
- AC : un prompt long envoyé depuis le mobile, téléphone verrouillé, une notification arrive quand une approbation est attendue ; la palette affiche le catalogue du manifeste ; un `go test ./...` lancé en one-shot affiche sa sortie complète.

## Jalon M5 : Durcissement (post-v1)

- [x] M5.1 Collecte semi-automatique des slash commands (`inspect` ouvre une session scratch, capture `/help`, suggère un diff de manifeste).
- [x] M5.2 Multi-runners simultanés, sélection de machine dans l'UI.
- [x] M5.3 Recherche dans l'historique de sessions.
- [x] M5.4 Rotation des tokens depuis l'UI (UI admin minimaliste ou endpoint scripté).

## Jalon M6 : Fournisseurs à jour, synchro auto des modèles, release v0.2.0

- [x] M6.1 Mécanisme `modelSync` (schéma manifeste + parsers `internal/modelsync` + message `models-sync` + endpoint `POST /api/agents/{id}/models/refresh` + sous-commande `agentpilot-runner models --write` + `scripts/sync-agent-models.sh`). ADR-0006.
- [x] M6.2 Manifeste `antigravity.json` corrigé : modèles réels du CLI `agy` (Gemini 4 Neon/Carbon/Argon/Hydrogen, Gemini 3.x, Orchestrator), vraies slash commands, option `--effort`, synchro via `agy models`.
- [x] M6.3 Nouveaux fournisseurs `claude.json` (Claude Code) et `codex.json` (Codex CLI, dont `continueHeadlessArgs` pour `codex exec resume --last`) ; `copilot.json` et `mistral.json` rafraîchis (modèles courants + `modelSync`).
- [x] M6.4 UI web : bouton de synchronisation des modèles dans le panneau fournisseur.
- [x] M6.5 Release v0.2.0 (tag `v0.2.0`, release GoReleaser + image Docker en CI).

## Engagements transverses

- Chaque PR : petits commits, tests idiomatiques au style du projet, mise à jour de la doc concernée.
- ADR dès qu'une décision structurante dévie du présent pack.
- Aucune exposition publique du backend : aucun mode réseau autre que LAN/Tailscale ; les secrets restent dans la configuration montée, jamais dans Git.
