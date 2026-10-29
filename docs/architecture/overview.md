# Vue d'ensemble

Ce document décrit l'architecture implémentée de la Phase 1 : un
control plane Go (API HTTP + contrôleur de workspaces) au-dessus d'un
data plane local (processus OS, filesystem, réseau).

## Plans

```text
Clients (CLI pc, page /terminal, HTTP)
        │
        ▼
Control plane                    Data plane (local)
─────────────────                ──────────────────
services/api                     runtime/container
  REST /v1/workspaces              local (défaut) · docker (CLI)
  WebSocket exec                 runtime/process
  page /terminal                   local (os/exec) · faux
services/workspace-controller    runtime/filesystem
  lifecycle + persistance JSON     local (répertoire) · mémoire
  bus d'événements               runtime/network
packages/protocol                  ports libres · passerelle (souche)
  enveloppe Event + EventBus
packages/types
  Workspace, Process, Snapshot…
```

## Flux principal

1. Le client appelle l'API (`POST /v1/workspaces`, `POST …/start`,
   `GET …/exec`…).
2. `services/api` valide la requête, délègue au contrôleur et traduit
   les erreurs en codes HTTP (400 validation, 404 inconnu, 409
   transition illégale, 500 interne).
3. Le contrôleur (`services/workspace-controller`) fait respecter les
   transitions d'état, persiste dans le magasin JSON
   (`controller.FileStore`, atomique) et pilote le backend de
   conteneurs.
4. Chaque transition réussie publie un événement typé sur le bus
   (`packages/protocol.EventBus`, en mémoire, synchronisé).
5. `cmd/api` assemble le tout : magasin JSON + backend local +
   bus, servis sur `:8080` (`-addr`, `-data`).

## Fichiers d'entrée

- `cmd/api/main.go` : point d'entrée du binaire API.
- `services/api/server.go` : routes HTTP et WebSocket.
- `services/workspace-controller/controller.go` : lifecycle.
- `apps/cli` : client `pc` (stdlib uniquement).

Voir aussi : `workspace-lifecycle.md`, `runtime-backends.md`,
`../protocols/events.md`, `terminal.md`.
