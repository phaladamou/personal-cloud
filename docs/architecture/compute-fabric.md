# Compute fabric (Phase 4)

Ce document décrit le fabric de compute de la Phase 4 : comment les
nœuds s'enregistrent et prouvent leur vie, comment l'ordonnanceur
choisit un nœud, comment les GPU sont modélisés et comment un
workspace est placé puis libéré.

Fichiers : `compute/node`, `compute/gpu`, `services/scheduler`,
`services/workspace-controller/placement.go`, `services/api/nodes.go`,
`cmd/api/main.go` (câblage + balayeur).

## Nœuds

Un nœud (`packages/types.Node`) est une machine capable d'exécuter
des workloads : capacité (`cpu`, `memory_mb`, `disk_gb`, `gpus`),
labels libres, état (`ONLINE`, `OFFLINE`, `DRAINED`), ressources
déjà attribuées (`Allocated`) et dernier battement (`LastHeartbeat`).

Le registre (`compute/node.Registry`, en mémoire, sûr pour un usage
concurrent) expose :

- `Register` : ajoute le nœud `ONLINE`, émet `node.connected`. Avec
  `Spec.ID` positionné, ré-enregistre cette identité : capacité et
  labels rafraîchis, `Allocated` conservé, retour `ONLINE`.
- `Heartbeat` : rafraîchit le battement ; un nœud `OFFLINE` repasse
  `ONLINE` (`node.connected`), un nœud `DRAINED` reste drainé.
- `Sweep` : passe `OFFLINE` les nœuds `ONLINE` dont le battement est
  plus ancien que le délai configuré (30 s par défaut,
  `WithHeartbeatTimeout`, horloge injectable via `WithClock`) et
  émet `node.disconnected` pour chacun.
- `Allocate` / `Release` : réserve et libère des ressources sur un
  nœud `ONLINE` (dépassement → `ErrInsufficientCapacity`, nœud non
  prêt → `ErrNodeNotReady`, libération clampée à zéro).
- `Find` : nœuds `ONLINE` dont l'allocatable couvre la demande et
  dont les labels satisfont le sélecteur, triés par id.

`Allocatable` vaut capacité moins attribué ; `Matches` teste le
sélecteur (un sélecteur vide accepte tout). Les événements `node.*`
sont à portée cluster : `workspace_id` vide, nœud en payload (voir
`../protocols/events.md`).

`cmd/api` fait tourner un balayeur (`runSweeper`, goroutine) qui
appelle `Sweep` toutes les `-sweep-interval` (10 s par défaut,
désactivé si non positif) avec une fenêtre de vie `-heartbeat-timeout`
(30 s par défaut) ; chaque nœud nouvellement hors-ligne est journalisé.

## Ressources

`packages/types.Resource` (`cpu`, `memory_mb`, `disk_gb`, `gpus`)
décrit indifféremment une capacité, une demande ou une attribution :

- `Fits` : la demande tient-elle dans la capacité, dimension par
  dimension (GPU compris) ;
- `Add` / `Sub` (soustraction clampée à zéro, une libération trop
  large ne rend jamais négatif) ;
- `Negative` : rejet des demandes négatives (`400` via l'API).

## Ordonnanceur

`services/scheduler.Scheduler` place une demande sur le registre :

```text
demande (Resource + sélecteur)
        │
        ▼
filtres durs (Registry.Find) : ONLINE, Fits sur l'allocatable
(GPU inclus), labels ⊇ sélecteur
        │
        ▼
score : le moins chargé gagne (somme des ratios utilisés /
capacité sur cpu, mémoire, disque, GPU ; une capacité nulle
compte comme idle) ; égalité → plus petit id (déterministe)
        │
        ▼
réservation via Allocate ; en cas de course perdue contre un
accès direct, essai du candidat suivant ; sinon ErrNoFit
```

`Schedule` sérialise les placements (mutex) pour que deux demandes
concurrentes observent leurs réservations mutuelles ; `Release`
libère une réservation. `ErrNoFit` (« no suitable node ») remonte
en `409 Conflict` via l'API.

## GPU

`compute/gpu` modélise les accélérateurs d'une machine :

- `Inventory` : liste de `Device` (`index`, `model`, `memory_mb`),
  avec `Count`, `TotalMemoryMB`, `Models` (noms uniques triés).
- `Discover` : construit l'inventaire local via `nvidia-smi`
  (borné à 5 s). Au mieux (« best-effort ») : binaire absent,
  délai dépassé ou sortie illisible donnent un inventaire vide,
  jamais une erreur — une machine sans GPU ne demande aucun cas
  spécial. `Fake` / `FakeDiscoverer` fournissent des inventaires
  scriptés pour les tests.
- Le placement GPU passe par le compteur `Resource.GPUs` (fit sur
  l'allocatable) ; `Labels` expose en plus l'inventaire en labels
  (`gpu.count` toujours, plus `gpu.model` et `gpu.memory_mb` quand
  l'inventaire ne contient qu'un seul modèle) afin d'épingler un
  modèle via un sélecteur.

## Flux de placement

```text
POST /v1/workspaces {…, cpu, memory_mb, disk_gb, gpus, selector}
        │  CreateWithResources : exigences enregistrées sur le workspace
        ▼
POST /v1/workspaces/{id}/start [mêmes champs]
 corps vide → Start (exigences de la création)
        │  place : Schedule + NodeID persisté AVANT le boot
        ▼
  STARTING → RUNNING (conteneur booté)
        │
        ├─ échec du boot → réservation libérée, retour STOPPED
        ├─ Stop → STOPPED + réservation libérée
        └─ Delete (CREATED, STOPPED, SLEEPING) → réservation libérée
```

- Sans exigences (`WantsPlacement` faux : ressources nulles et
  sélecteur vide) ou sans ordonnanceur configuré, le placement est
  un no-op : le workspace démarre non placé sur le backend local.
- `place` libère d'abord un `NodeID` périmé (re-placement après
  `Stop` sans fuite) ; en cas d'échec (`ErrNoFit`, magasin), le
  workspace reste intact (statut et placement inchangés).
- `Restore` replace comme `Start` ; `Sleep` conserve sa réservation.
- Le `NodeID` assigné est persisté avec le workspace et visible
  dans la colonne `NODE` de la CLI (`-` si non placé).

## Routes et CLI

| Route                                  | Effet                                    |
| -------------------------------------- | ---------------------------------------- |
| `POST /v1/nodes`                       | enregistre un nœud (`201`)               |
| `GET /v1/nodes`, `GET /v1/nodes/{nid}` | liste / détail                           |
| `DELETE /v1/nodes/{nid}`               | désenregistre (`204`)                    |
| `POST /v1/nodes/{nid}/heartbeat`       | battement de vie                         |
| `POST /v1/nodes/{nid}/{allocate,release}` | réserve / libère (`409` si trop juste) |

Erreurs : `404` nœud inconnu, `409` capacité insuffisante ou nœud
non prêt, `400` JSON ou demande invalide, `501` sans registre.

```bash
pc node register gpu-box --cpu 16 --mem 32768 --disk 200 --gpus 1 --label zone=east
pc node list   # ID, NAME, STATUS, CPU, MEMORY_MB, DISK_GB, GPUS
pc workspace create job --cpu 2 --mem 4096 --disk 20 --gpus 1 --selector zone=east
pc workspace start <id>   # place puis boote ; 409 si rien ne convient
```
