# AGENTS.md : Instructions de travail pour l'agent de codage

Ce fichier instruit un agent travaillant en autonomie sur le projet AgentPilot. À placer à la racine du repository au bootstrap.

## Mission

Construire AgentPilot : backend Go + runner Go multiplateforme + app mobile Flutter permettant de piloter des agents CLI (Copilot CLI, Antigravity CLI, Mistral CLI) installés sur les machines de l'homelab, depuis un téléphone, via LAN/Tailscale. Les spécifications font foi par ordre de priorité : PRD (`docs/PRD.md`) → ADR (`docs/adr/`) → backlog (`docs/BACKLOG.md`).

## Positionnement dans le repo

- Mono-repo : `server/` (module Go : `cmd/server`, `cmd/runner`, `internal/`), `app/` (Flutter), `docs/` (PRD, ADR, backlog, manifestes), `deploy/` (Dockerfile, compose, exemple de config).
- Le présent fichier vit à la racine.

## Stack et conventions

- Server/Runner : Go (dernier stable, 1.23+), `CGO_ENABLED=0`, `go vet ./...` et `golangci-lint` bloquants, routeur HTTP stdlib (`net/http` Go 1.22+) ou `chi`, WebSocket (`github.com/coder/websocket`), SQLite pure Go (`modernc.org/sqlite`) + migrations SQL (`github.com/pressly/goose/v3` avec `embed.FS`), tests `testing` stdlib. Privilégier la stdlib et les solutions simples ; pas de framework lourd ni de bibliothèque spéculative.
- Bridge PTY : lib multiplateforme éprouvée pour Windows (ConPTY) + Linux (PTY Unix) à la fois (le runner Windows et Linux doivent se comporter à l'identique) : `github.com/aymanbagabas/go-pty` (ou `github.com/gookit/gopty`, qui unifient `creack/pty` sur Unix et ConPTY sur Windows) ou une interface interne `PtySession` documentée dans un ADR si le choix change.
- Mobile : Flutter (Android), Dart 3, state management léger et répandu (Riverpod), émulateur terminal : package `xterm`. Tests : `flutter_test` + tests widget sur les écrans clés.
- Manifestes d'agents : JSON versionnés dans `docs/AgentManifests/`, schéma écrit (un `schema.json` + struct Go `encoding/json` validée en test).
- Chaque décision structurante contraire aux ADR existants : nouvel ADR, numérotation séquentielle, jamais réécrire un ADR accepté en silence.

## Boucle de travail attendue

1. Prendre le premier item non coché du jalon courant dans `docs/BACKLOG.md`.
2. Créer une branche `feat/mX.Y-description-courte`. Commits petits, atomiques, impératif (« Add », « Fix »).
3. Implémenter + tests (unitaires idiomatiques au style existant du projet ; test d'intégration quand un jalon l'exige).
4. Vérifier : `go vet ./...`, `go build ./...`, `go test ./...` verts (`golangci-lint run` si configuré), `flutter analyze` et `flutter test` verts.
5. Mettre à jour la doc impactée (backlog : cocher avec le numéro de PR ; ADR si décision).
6. PR vers la branche principale, description avec critères d'acceptation vérifiés un par un.

## Rails de sécurité (non négociables)

- Le backend n'écoute jamais sur `0.0.0.0` par défaut : bind LAN/tailnet documenté dans `deploy/`, et jamais d'exposition Internet (pas de tunnel public, pas de reverse proxy public, pas de port publié).
- Toute requête sans Bearer valide → 401. Pas d'exception « pour le dev » qui survive en prod.
- Aucun secret dans le code ou Git : configuration par environnement/volume, documentée dans `deploy/README.md`.
- Le runner exécute des commandes arbitraires par construction : s'assurer qu'il ne peut être joint que via le backend (aucun socket local ouvert, pas de mode debug réseau activé par défaut).
- Jamais modifier les credentials/auth des CLIs : l'application ne stocke, ne proxy et ne gère aucun token fournisseur (GitHub/Google/Mistral).

## Definition of Done d'un item

- Critères d'acceptation du backlog item vérifiés et cités dans la PR.
- Tests passant en CI ; pas de warning introduit.
- Doc à jour (backlog, ADR si applicable, `deploy/README.md` si le déploiement change).
- Les éléments de configuration ajoutés sont documentés une seule fois, à leur endroit canonique.

## Cadre d'autonomie

- Si un point est ambigu entre PRD/ADR/backlog, choisir l'option la plus simple et l'écrire dans la PR ; ne pas ouvrir de question bloquante pour des détails d'implémentation.
- Si une exigence du pack s'avère techniquement erronée ou impraticable, ne pas la contourner en silence : écrire un ADR « Proposition » dans la PR correspondante et l'implémenter de la façon retenue.
