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
- `workspace_id` : identifiant du workspace concerné. Obligatoire,
  sauf pour les événements `node.*` (portée cluster) où il reste vide
  et le nœud voyage dans le payload.
- `timestamp` : horodatage UTC à la milliseconde.
- `payload` : données spécifiques en JSON brut (optionnel) ;
  `DecodePayload` le désérialise.

## Types définis

| Événement            | Émetteur actuel                                  | Payload                                                      |
| -------------------- | ------------------------------------------------ | ------------------------------------------------------------ |
| `workspace.created`  | contrôleur (`Create`)                            | `{"status": "CREATED"}`                                      |
| `workspace.started`  | contrôleur (`Start`, `Wakeup`, `Restore`)        | `{"status": "RUNNING"}`                                      |
| `workspace.stopped`  | contrôleur (`Stop`)                              | `{"status": "STOPPED"}`                                      |
| `process.started`    | contrôleur (`Exec`)                              | `{"process_id": "exec-…", "command": ["echo", "hi"]}`        |
| `process.stdout`     | contrôleur (`Exec`, par morceau de sortie)       | `{"process_id": "exec-…", "data": "hi\n"}`                   |
| `process.exited`     | contrôleur (`Exec`)                              | `{"process_id": "exec-…", "exit_code": 0}` (+ `error` si échec) |
| `file.changed`       | contrôleur (`WriteFile`, `Mkdir`, `DeleteFile`)  | `{"path": "a/b.txt", "op": "write\|mkdir\|delete"}`          |
| `snapshot.created`   | contrôleur (`CreateSnapshot`)                    | le snapshot                                                  |

Les événements `node.*` suivent les transitions de connectivité :
`Register` (y compris le ré-enregistrement d'un identifiant connu)
et `Heartbeat` sur un nœud `OFFLINE` émettent `node.connected` ;
`Unregister` et `Sweep` (battement plus ancien que le délai
configuré, 30 s par défaut) émettent `node.disconnected`. Un second
`Sweep` sans nouveau battement reste silencieux.
| `node.connected`     | registre des nœuds (`Register`, `Heartbeat`)     | le nœud                                                      |
| `node.disconnected`  | registre des nœuds (`Unregister`, `Sweep`)       | le nœud                                                      |

Les dix types ont un émetteur. Les trois événements `process.*`
décrivent les commandes `Exec` : `process.started` avant l'exécution,
un `process.stdout` par morceau de sortie (stdout et stderr combinés),
puis `process.exited` avec le code de sortie (`-1` et un message
`error` en cas d'échec). L'`process_id` partagé est aussi celui des
listes de processus et des logs (`GET …/processes`,
`GET …/processes/{pid}/logs`).

## Bus (`EventBus`)

- En mémoire, sûr pour un usage concurrent.
- `Subscribe(type, handler)` retourne une fonction de
  désinscription ; `Publish(event)` notifie les abonnés du type.
- La publication est synchrone et au mieux (« best-effort ») : le
  contrôleur reste utilisable avec un bus `nil`.
