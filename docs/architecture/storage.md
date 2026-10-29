# Stockage persistant

Ce document décrit le stockage de la Phase 2 : des volumes
persistants par workspace (`storage/volumes`) et des snapshots à
adressage par contenu (`storage/snapshots`), orchestrés par le
contrôleur et exposés via l'API et la CLI.

## Disposition sur disque

`cmd/api` regroupe tout le stockage sous une racine unique
(`-storage`, `./data/storage` par défaut) :

```text
<storage>/
├── volumes/      # LocalManager des volumes (+ fichiers de l'API files)
└── snapshots/    # LocalManager des snapshots
```

L'API files et les snapshots partagent le même support : les
opérations files sont enracinées sur le répertoire `data` du volume
`default` du workspace (`VolumeFSProvider`). Un snapshot capture
donc exactement ce que `pc files put` dépose, et un restore ramène
les fichiers (prouvé par `TestFilesSurviveSnapshotRestore` et le e2e
put → snapshot → rm → restore → cat).

## Volumes (`storage/volumes`)

Un volume est un filesystem nommé appartenant à un workspace
(`packages/types.Volume` : `id`, `workspace_id`, `name`, `size_gb`,
`mount_path`, `created_at`).

Interface `VolumeManager` : `Create`, `Attach` / `Detach` (idempotents)
+ `Attached`, `Delete` (supprime aussi les données), `Inspect`,
`List` (triés par id ; un workspace inconnu liste vide, pas d'erreur).
Toutes les opérations sont scopées par `workspaceID`, et les
implémentations sont sûres pour un usage concurrent.

- `local` (`LocalManager`) : un volume =
  `<root>/<workspace>/volumes/<volume>/{meta.json,data/}`.
  `meta.json` est écrit atomiquement (fichier temporaire + renommage)
  ; `DataPath(workspaceID, volumeID)` retourne le répertoire `data/`
  qui porte le contenu.
- `fake` (`FakeManager`) : en mémoire, pour les tests.

## Snapshots (`storage/snapshots`)

Un snapshot est une copie ponctuelle d'un volume
(`packages/types.Snapshot` : `id`, `workspace_id`, `volume_id`,
`name`, `size_bytes`, `created_at`).

Interface `SnapshotManager` : `Create`, `List` (triés par date de
création puis id ; un workspace inconnu liste vide), `Get`, `Delete`
(les données du volume sont intactes), `Restore` (remplace le contenu
du volume enregistré dans les métadonnées).

- Adressage par contenu : l'id vaut `snap-` + les 16 premiers
  caractères hexadécimaux du SHA-256 de l'arbre (chemins relatifs
  triés, modes et contenus). Créer un snapshot d'un contenu inchangé
  retourne le snapshot existant au lieu de stocker un doublon.
- Stratégie de copie : chaque snapshot est une copie complète
  indépendante, publiée atomiquement (répertoire de staging +
  renommage). Le copy-on-write par hardlinks a été délibérément
  écarté : les écrivains in-place (`os.WriteFile` tronque sa cible)
  corrompraient tous les snapshots partageant l'inode. `Restore` est
  lui aussi une copie complète, pour qu'aucune écriture ultérieure ne
  partage de données avec un snapshot.
- Disposition : `<root>/<workspace>/<snapshot>/` (+ `meta.json`) ;
  le staging utilise `<workspace>/.staging-<id>-…`.
- `fake` (`FakeManager`) : en mémoire, pour les tests.

## Câblage contrôleur

`services/workspace-controller` (fichier `storage.go`) :

- `WithVolumes` / `WithSnapshots` injectent les managers ; sans eux,
  les opérations retournent `ErrVolumesUnavailable` /
  `ErrSnapshotsUnavailable` (traduits en `501` par l'API).
- Volume `default` (10 Go, monté sur `/data`) provisionné
  paresseusement quand un snapshot ne nomme pas de volume explicite.
- `CreateSnapshot`, autorisé dans tout état du workspace, émet
  `snapshot.created` avec le snapshot en payload (voir
  `../protocols/events.md`).
- `RestoreSnapshot` ne touche jamais au statut du workspace
  (contrairement à `Restore`, l'opération de cycle de vie).
- `Delete` nettoie les volumes et snapshots au mieux (« best-effort ») :
  un reliquat de stockage ne fait jamais échouer la suppression.

## API et CLI

Routes (`services/api/snapshots.go`) :

```text
POST   /v1/workspaces/{id}/snapshots
GET    /v1/workspaces/{id}/snapshots
GET    /v1/workspaces/{id}/snapshots/{sid}
POST   /v1/workspaces/{id}/snapshots/{sid}/restore
DELETE /v1/workspaces/{id}/snapshots/{sid}
```

CLI (`apps/cli/snapshot.go`) : `pc snapshot create <id> [--volume ID]
[--name NAME]`, `list`, `restore`, `delete`.

## Convergence files / volumes

Historiquement, l'API files écrivait sous `<storage>/workspaces/<id>/`
(`DirFSProvider`) pendant que les snapshots capturaient
`<storage>/volumes/…` : un restore ne ramenait pas les fichiers. La
convergence adosse les opérations files au volume `default`
(`VolumeFSProvider`), avec provisionnement paresseux identique à
celui des snapshots. `DirFSProvider` subsiste pour les usages sans
volumes (tests, binaires sans stockage).
