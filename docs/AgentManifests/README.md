# Manifestes d'agents

Un fichier JSON par agent CLI supporté, conforme à `schema.json` (validé au chargement par `manifests.LoadDir` et par le test `TestLoadDirInitialAgentManifests`).

## Mise à jour automatique des modèles (`modelSync`)

Chaque agent CLI déclare un bloc `modelSync` qui décrit comment découvrir la liste réelle des modèles exposés par le CLI :

```json
"modelSync": {
  "optionId": "model",
  "args": ["models"],
  "parser": "columns",
  "timeoutSec": 20
}
```

- `args` : arguments exécutés en non-interactif (ex. `agy models`, `copilot --help`, `codex debug models`).
- `send` : repli écrit dans une session PTY scratch (ex. `"/model\n"`) si `args` ne produit rien.
- `parser` : `columns` (lignes `slug   Libellé`), `help-flag` (section d'un flag de `--help`, défaut `--model`), `json`, `lines`, `regex` (avec `pattern`).
- `sendParser` : parseur utilisé pour le repli `send` quand il diffère de `parser`.

Deux chemins de mise à jour :

1. **À chaud, par machine** : `POST /api/agents/{id}/models/refresh?machine=<nom>` (backend → runner via le message `models-sync`) parse la sortie du CLI installé sur la machine et persiste les choix dans le manifeste en base. L'UI web expose un bouton « synchroniser » sur l'option concernée.
2. **Dans le repo** : `scripts/sync-agent-models.sh` exécute `agentpilot-runner models --manifest docs/AgentManifests/<agent>.json --write` pour chaque CLI installé localement et réécrit les `choices` du fichier.

Règle : la pseudo-choix `default` (« ne pas passer le flag ») et le `defaultValue` declared sont toujours conservés en tête de liste.
