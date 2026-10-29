# Cycle de vie des workspaces

Le workspace est l'unité fondamentale du système. Son état
(`packages/types.WorkspaceStatus`) ne peut évoluer que selon les
transitions déclarées dans `ValidWorkspaceTransitions`.

## Transitions autorisées

| Depuis    | Vers autorisés       |
| --------- | -------------------- |
| CREATED   | STARTING             |
| STARTING  | RUNNING, STOPPED     |
| RUNNING   | SLEEPING, STOPPED    |
| SLEEPING  | RUNNING, STOPPED     |
| STOPPED   | STARTING, RESTORING  |
| RESTORING | RUNNING, STOPPED     |

Toute autre transition est rejetée (`409 Conflict` via l'API).

## Opérations du contrôleur

`services/workspace-controller.Controller` expose :

- `Create` : crée le workspace en CREATED, émet `workspace.created`.
- `Start` : CREATED/STOPPED → STARTING → RUNNING (démarre le
  conteneur), émet `workspace.started`. Si le démarrage du conteneur
  échoue, le workspace retombe en STOPPED.
- `Sleep` : RUNNING → SLEEPING (conteneur arrêté mais conservé).
- `Wakeup` : SLEEPING → RUNNING (redémarre le conteneur), émet
  `workspace.started`.
- `Stop` : RUNNING/SLEEPING/STARTING → STOPPED, émet
  `workspace.stopped`. Un conteneur manquant compte comme déjà arrêté.
- `Restore` : STOPPED → RESTORING → RUNNING, émet
  `workspace.started`. En cas d'échec, retour en STOPPED.
- `Delete` : supprime un workspace CREATED, STOPPED ou SLEEPING
  ainsi que son conteneur.
- `Exec` : exécute une commande dans le conteneur du workspace, en
  émettant `process.started`, `process.stdout` puis `process.exited`.
- `Get`, `List` : lecture depuis le magasin.

## Persistance

Le magasin JSON (`controller.FileStore`) écrit chaque état de façon
atomique (fichier temporaire + renommage) avec une enveloppe
versionnée. Les clients CLI et HTTP observent donc le même état
(`GET /v1/workspaces/{id}`), indépendamment de l'appareil utilisé.
