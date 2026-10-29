# Catalogue des événements

Les composants communiquent via des événements typés
(`packages/protocol`), jamais via leurs implémentations internes.

## Enveloppe

```json
{
  "type": "workspace.started",
  "workspace_id": "ws-123",
  "timestamp": "2026-10-29T12:34:56.789Z",
  "payload": {"status": "RUNNING"}
}
```

- `type` : l'un des 10 noms ci-dessous (validé à la construction
  comme au décodage).
- `workspace_id` : identifiant du workspace concerné (obligatoire).
- `timestamp` : horodatage UTC à la milliseconde.
- `payload` : données spécifiques en JSON brut (optionnel) ;
  `DecodePayload` le désérialise.

## Types définis

| Événement          | Émetteur actuel              | Payload                    |
| ------------------ | ---------------------------- | -------------------------- |
| `workspace.created`  | contrôleur (`Create`)      | `{"status": "CREATED"}`    |
| `workspace.started`  | contrôleur (`Start`, `Wakeup`, `Restore`) | `{"status": "RUNNING"}` |
| `workspace.stopped`  | contrôleur (`Stop`)        | `{"status": "STOPPED"}`    |
| `process.started`    | réservé (Phase 2+)         | —                          |
| `process.stdout`     | réservé (Phase 2+)         | —                          |
| `process.exited`     | réservé (Phase 2+)         | —                          |
| `file.changed`       | réservé (Phase 2+)         | —                          |
| `snapshot.created`   | réservé (Phase 2+)         | —                          |
| `node.connected`     | réservé (Phases 4/6)       | —                          |
| `node.disconnected`  | réservé (Phases 4/6)       | —                          |

Seuls les trois événements du cycle de vie des workspaces sont émis
aujourd'hui ; les autres noms sont réservés pour que les futurs
émetteurs partagent le même contrat.

## Bus (`EventBus`)

- En mémoire, sûr pour un usage concurrent.
- `Subscribe(type, handler)` retourne une fonction de
  désinscription ; `Publish(event)` notifie les abonnés du type.
- La publication est synchrone et au mieux (« best-effort ») : le
  contrôleur reste utilisable avec un bus `nil`.
