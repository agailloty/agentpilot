# PRD : AgentPilot (nom de code provisoire)

Pilotage mobile d'agents IA en ligne de commande installés sur les machines de l'homelab.

## 1. Contexte et problème

Les agents CLI suivants sont installés et authentifiés sur le PC de l'utilisateur :

- GitHub Copilot CLI (agent terminal GitHub)
- Antigravity CLI (agent terminal Google)
- Mistral CLI / Mistral Code (agent terminal Mistral)

Ces outils sont des TUI interactifs : conversation continue, slash commands, demandes d'approbation, exécution de commandes, modification de fichiers. L'utilisateur passe une part importante de son temps sur téléphone et ne peut pas se connecter en SSH à son PC à chaque besoin. Perdre l'accès à ces agents hors du bureau diminue fortement la valeur de l'installation.

## 2. Objectif produit

Une application mobile native, optimisée pour un usage à une main, qui permet de :

1. Déléguer des tâches à un agent CLI via une zone de saisie de prompt.
2. Créer, depuis le mobile, un dossier projet sur la machine cible, et borner la session de l'agent à ce dossier (working directory = racine de session).
3. Piloter l'agent comme depuis un terminal : voir le flux complet de la session, envoyer des prompts, répondre aux approbations.
4. Découvrir et utiliser toutes les capacités natives de chaque CLI : palette de slash commands, raccourcis, modes.

L'expérience cible : « comme si j'étais devant VS Code avec mon agent dans le terminal », mais sur téléphone.

## 3. Utilisateur et usage

Utilisateur unique (l'owner). Pas de multi-utilisateur en v1.

Parcours types :

- **Délégation rapide** : ouvrir l'app, choisir un agent et un dossier, taper un prompt, suivre l'exécution, répondre à une approbation, quitter. Mobilité : chaque interaction doit être possible à une main.
- **Lancement de projet** : depuis l'app, créer un dossier sur la machine cible, ouvrir une session dedans, demander la génération d'un projet, code review, etc.
- **Suivi passif** : envoyer une tâche longue, verrouiller le téléphone, être notifié quand l'agent a terminé ou attend une approbation.
- **Relance one-shot** : sur un projet existant, lancer une micro-commande unique (test, lint, git status) sans ouvrir une session interactive complète.

## 4. Périmètre fonctionnel v1

### 4.1 Gestion des projets
- Créer un dossier projet sur une machine cible (chemin parent configurable, nom saisi, éventuellement `git init` optionnel).
- Lister les projets existants (dossiers balayés sous des racines déclarées).
- Supprimer / archiver un projet.
- Un projet est le conteneur des sessions : toute session ouverte sur un projet a ce dossier comme working directory et comme périmètre.

### 4.2 Sessions d'agent
- Ouvrir une session : choix agent × projet.
- Session interactive persistante : le processus de l'agent reste vivant pendant que l'app est en arrière-plan ou fermée ; l'utilisateur retrouve l'état exact en revenant (reprise du stream).
- Vue terminal : rendu fidèle du flux ANSI de la session (le CLI garde son TUI natif complet).
- Zone de saisie : prompt + envoi vers la session ; autocomplétion des slash commands de l'agent.
- Palette de slash commands : liste par agent, description courte, envoi en un tap.
- Approbations : détection des demandes de confirmation de l'agent, boutons rapides Approuver / Refuser (+ saisie custom).
- Clôture de session explicite ; arrêt propre du processus de l'agent.
- Historique des sessions archivées (transcript brut consultable).

### 4.3 Relances one-shot (hybrides)
- Mode « exécution ponctuelle » : une commande unique exécutée dans le dossier du projet (commande shell arbitraire autorisée, ex. `go test ./...`), sortie renvoyée en texte.
- Séparation claire dans l'UI entre session d'agent et commande shell.

### 4.4 Découverte des capacités
- Catalogue par agent importé d'un fichier manifeste versionné (slash commands, description, arguments éventuels).
- Évolution ultérieure (hors v1) : collecte semi-automatique des commandes (capture de `/help` dans une session scratch).

### 4.5 Sécurité (voir ADR-0004)
- Accès uniquement via Tailscale ou LAN ; le backend n'est jamais exposé publiquement.
- Token applicatif obligatoire pour l'app comme pour les runners.

## 5. Hors périmètre v1

- Multi-utilisateur, gestion des rôles.
- Rendu « chat structuré » des réponses des agents (bulles de messages parsées) : v1 = rendu terminal fidèle. L'extraction structurée ne sera considérée qu'en v2.
- Conférence d'accès public (tunnel HTTPS public, reverse proxy Internet).
- Agents s'exécutant dans le cloud.
- Partage de sessions, collaboration multi-appareils simultanés.

## 6. Critères de succès

1. Depuis un téléphone sur Tailscale, créer un dossier et y faire générer un projet complet par Copilot CLI, avec au moins une approbation gérée depuis le mobile, sans SSH.
2. Latence de frappe perçue < 300 ms sur LAN ; < 800 ms via Tailscale.
3. Une session survit à la fermeture de l'app et à un cycle veille/réveil du téléphone.
4. Les slash commands du manifeste apparaissent en autocomplétion et s'exécutent en un tap.
5. Un one-shot (`go test ./...`) renvoie sa sortie complète dans l'UI mobile.

## 7. Contraintes

- Backend & Runner : Go (binaire statique sans CGO, conteneur Docker léger déployable n'importe où sur l'homelab ; runner natif Windows/Linux sans runtime à installer).
- Mobile : Flutter (Android en priorité ; iOS non requis en v1).
- Réseau : LAN ou Tailscale ; token applicatif ; jamais d'exposition publique.
- L'utilisateur réel des CLIs reste la personne physique : les CLIs sont installés, configurés et authentifiés sur ses machines ; l'application ne gère pas les credentials des fournisseurs d'IA (GitHub/Google/Mistral) autrement que par lancement de `login` interactif dans une session si nécessaire.

## 8. Risques connus

| Risque | Mitigation |
| --- | --- |
| Les TUI rendent difficilement sur mobile (scroll, ANSI complexes) | Émulateur de terminal éprouvé côté Flutter (package `xterm`) plutôt que parsing des sorties |
| Approbations manquées si l'utilisateur quitte l'app | Notification push (ntfy auto-hébergé) à la détection d'un prompt de confirmation |
| Slash commands hétérogènes entre agents | Manifeste versionné par agent, validé manuellement |
| Un agent peut exécuter des commandes arbitraires | Le périmètre = dossier projet ; l'UI montre toujours le working directory du flux ; approbations explicites |
| Déconnexions Tailscale | Session serveur persistante ; reconnexion transparente du client mobile |
