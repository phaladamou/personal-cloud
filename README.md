# Personal Cloud

> **Your computer is an environment, not a device.**

Personal Cloud est une infrastructure de **cloud computing personnel persistant** conçue pour transformer l'ordinateur personnel en un environnement informatique indépendant du terminal physique.

L'idée est simple :

**tes fichiers, ton environnement de développement, tes processus, tes services, ton compute et, à terme, tes agents IA ne doivent pas être prisonniers d'un ordinateur particulier.**

Tu dois pouvoir commencer une session sur un laptop, continuer depuis un autre ordinateur ou un téléphone, et retrouver le même environnement.

---

## Vision

Aujourd'hui, l'ordinateur personnel est principalement défini par une machine physique :

```text
┌─────────────────────────┐
│       Laptop            │
│                         │
│ CPU                     │
│ RAM                     │
│ SSD                     │
│ Applications            │
│ Files                   │
└─────────────────────────┘
```

Personal Cloud propose une autre abstraction :

```text
                 Your Identity
                       │
                       ▼
              ┌─────────────────┐
              │ Personal Cloud  │
              │    Computer     │
              └────────┬────────┘
                       │
        ┌──────────────┼──────────────┐
        ▼              ▼              ▼
     Compute         Storage        Network
        │              │              │
       CPU            Files         Browser
       GPU            Data          Services
        │              │              │
        └──────────────┼──────────────┘
                       ▼
                  AI Runtime
```

Le terminal devient alors une **fenêtre vers l'environnement**, plutôt que l'environnement lui-même.

---

# Core Principle

> **The personal computer should be a persistent computational identity, not a physical device.**

Un appareil peut disparaître.

L'environnement doit continuer d'exister.

```text
Laptop destroyed
      │
      ▼
Personal Cloud
      │
      ▼
New device
      │
      ▼
Same environment
```

---

# What Personal Cloud Is

Personal Cloud cherche à fournir un environnement informatique personnel comprenant progressivement :

* un workspace persistant ;
* du compute CPU et GPU ;
* un filesystem persistant ;
* des processus et services ;
* un terminal distant ;
* un réseau programmable ;
* des snapshots ;
* plusieurs appareils clients ;
* une API de contrôle ;
* un SDK ;
* une CLI ;
* un runtime IA ;
* une mémoire persistante pour les systèmes IA ;
* un scheduler capable de déplacer les workloads entre différentes ressources.

L'objectif n'est pas de créer simplement une interface autour d'un VPS.

Personal Cloud est pensé comme une **couche d'abstraction informatique personnelle**.

---

# Architecture

Le repository est organisé autour de plusieurs plans : interface, contrôle, exécution, compute, stockage, intelligence et infrastructure.

```text
personal-cloud/
│
├── apps/
│   ├── web/
│   ├── desktop/
│   └── cli/
│
├── services/
│   ├── api/
│   ├── workspace-controller/
│   ├── scheduler/
│   ├── gateway/
│   └── auth/
│
├── runtime/
│   ├── container/
│   ├── process/
│   ├── filesystem/
│   └── network/
│
├── compute/
│   ├── node/
│   ├── resources/
│   └── gpu/
│
├── storage/
│   ├── volumes/
│   ├── snapshots/
│   └── objects/
│
├── ai/
│   ├── runtime/
│   ├── tools/
│   ├── memory/
│   └── planner/
│
├── packages/
│   ├── protocol/
│   ├── sdk/
│   └── types/
│
├── infra/
│   ├── docker/
│   ├── kubernetes/
│   └── terraform/
│
└── docs/
    ├── architecture/
    ├── protocols/
    └── security/
```

---

# Architecture Principles

## 1. Workspace First

Le **Workspace** est l'unité fondamentale de Personal Cloud.

Un workspace représente l'environnement informatique d'un utilisateur.

Il peut contenir :

```text
Workspace
├── filesystem
├── processes
├── services
├── network
├── environment
├── compute resources
└── persistent state
```

Le workspace doit pouvoir survivre à la déconnexion d'un utilisateur, au changement d'appareil et, à terme, au déplacement entre différentes machines de compute.

---

## 2. Control Plane / Data Plane

Personal Cloud sépare le système de contrôle de l'exécution réelle.

### Control Plane

Le control plane décide :

* quel workspace créer ;
* où l'exécuter ;
* quelles ressources lui attribuer ;
* quand le suspendre ;
* quand le restaurer ;
* quelles politiques appliquer.

```text
API
 │
 ▼
Workspace Controller
 │
 ├── Scheduler
 ├── Resource Manager
 └── Policy Engine
```

### Data Plane

Le data plane exécute réellement les workloads :

```text
Compute Node
     │
     ▼
Runtime
     │
     ▼
Container / MicroVM
     │
     ├── Processes
     ├── Filesystem
     └── Network
```

Cette séparation permet de faire évoluer l'architecture sans coupler la logique de contrôle au runtime d'exécution.

---

# Repository Structure

## `apps/`

Les applications destinées directement aux utilisateurs.

### `apps/web`

Interface web permettant notamment :

* de créer un workspace ;
* consulter son état ;
* ouvrir un terminal ;
* explorer les fichiers ;
* gérer les services ;
* visualiser les ressources ;
* contrôler les sessions.

### `apps/desktop`

Client desktop destiné à fournir une intégration plus profonde avec l'environnement local.

À terme, il pourra permettre notamment :

* l'accès aux fichiers locaux ;
* l'utilisation du compute local ;
* la synchronisation ;
* le mode hybride cloud/local.

### `apps/cli`

Interface en ligne de commande.

Exemples futurs :

```bash
pc login
pc workspace create
pc workspace list
pc workspace connect
pc workspace stop
pc workspace snapshot
pc workspace restore
pc exec
pc logs
```

---

# `services/`

Les services du control plane.

## `services/api`

Point d'entrée API du système.

Responsabilités :

* authentication ;
* workspace management ;
* sessions ;
* resources ;
* snapshots ;
* jobs ;
* API publique.

---

## `services/workspace-controller`

Le cerveau du lifecycle des workspaces.

Il gère notamment :

```text
CREATE
   ↓
START
   ↓
RUNNING
   ↓
SLEEPING
   ↓
STOPPED
   ↓
RESTORING
   ↓
RUNNING
```

Le controller ne doit pas être responsable de l'exécution bas niveau elle-même.

Il orchestre le runtime.

---

## `services/scheduler`

Le scheduler décide où exécuter les workloads.

Exemple :

```text
Workload
   │
   ├── CPU requirement
   ├── Memory requirement
   ├── GPU requirement
   ├── Storage requirement
   └── locality
        │
        ▼
     Scheduler
        │
   ┌────┼────┐
   ▼    ▼    ▼
 Node A B    C
```

À terme, il pourra prendre en compte :

* disponibilité ;
* capacité ;
* coût ;
* GPU ;
* mémoire ;
* localisation ;
* charge ;
* contraintes du workspace.

---

## `services/gateway`

Couche réseau entre les clients et les workloads.

Responsabilités possibles :

* routing ;
* WebSocket ;
* terminal streaming ;
* service discovery ;
* port forwarding ;
* reverse proxy ;
* connection management.

---

## `services/auth`

Gestion de l'identité et des permissions.

Le système doit considérer l'identité comme une primitive fondamentale :

```text
Identity
   │
   ├── Devices
   ├── Workspaces
   ├── Sessions
   ├── Credentials
   └── Permissions
```

---

# `runtime/`

Le runtime transforme un workspace abstrait en environnement réellement exécutable.

## `runtime/container`

Gestion des containers et, à terme, des microVMs.

Le développement pourra commencer avec des containers puis évoluer vers des primitives d'isolation plus fortes.

---

## `runtime/process`

Gestion des processus :

* lancement ;
* arrêt ;
* signaux ;
* logs ;
* exit codes ;
* monitoring.

---

## `runtime/filesystem`

Abstraction du filesystem du workspace.

Elle doit permettre notamment :

```text
read
write
move
delete
watch
snapshot
restore
```

---

## `runtime/network`

Gestion réseau du workspace :

* ports ;
* interfaces ;
* routing ;
* isolation ;
* service discovery ;
* connections.

---

# `compute/`

Couche représentant les ressources physiques ou virtuelles disponibles.

## `compute/node`

Un node est une machine capable d'exécuter des workloads.

```text
Node
├── CPU
├── Memory
├── Storage
├── Network
└── GPU
```

Un node peut être :

* une machine cloud ;
* un serveur ;
* une machine personnelle ;
* un GPU server ;
* à terme, un appareil local.

---

## `compute/resources`

Abstraction des ressources.

Exemple :

```text
CPU
Memory
Disk
Bandwidth
GPU
```

Cette couche permet au scheduler de raisonner en termes de ressources plutôt qu'en termes de machines spécifiques.

---

## `compute/gpu`

Gestion des accélérateurs.

À terme :

```text
Workspace
     │
     ▼
GPU requirement
     │
     ▼
Scheduler
     │
     ▼
Available GPU node
```

---

# `storage/`

Le stockage est séparé du runtime afin que l'environnement puisse survivre au cycle de vie d'une machine.

## `storage/volumes`

Volumes persistants.

```text
Workspace
   │
   └── Persistent Volume
```

Un workspace peut ainsi être détruit puis recréé sans perdre ses données.

---

## `storage/snapshots`

Snapshots de l'état du workspace.

Vision à long terme :

```text
Workspace V1
     │
  Snapshot
     │
Workspace V2
     │
  Snapshot
     │
Workspace V3
```

Cela ouvre la possibilité d'un futur modèle proche de :

> **Git for computational state.**

---

## `storage/objects`

Stockage des objets volumineux et données persistantes.

Cette couche pourra notamment s'appuyer sur un object storage compatible S3.

---

# `ai/`

L'IA est une couche native du système, mais elle ne définit pas l'identité fondamentale du projet.

Le principe est :

> **AI should be able to operate the computer because the computer is programmable.**

## `ai/runtime`

Runtime permettant d'exécuter des workloads IA.

---

## `ai/tools`

Outils permettant à une IA d'interagir avec le système :

```text
read_file()
write_file()
run_command()
inspect_process()
open_browser()
start_service()
create_workspace()
snapshot_workspace()
```

---

## `ai/memory`

Mémoire persistante permettant aux systèmes IA de conserver du contexte et de l'expérience au-delà d'une session.

---

## `ai/planner`

Planification et orchestration des tâches complexes.

À terme :

```text
Goal
 ↓
Planner
 ↓
Tasks
 ↓
Tools
 ↓
Computer
 ↓
Observation
 ↓
Next action
```

---

# `packages/`

Les contrats partagés du système.

Cette partie est particulièrement importante.

## `packages/types`

Types communs :

```text
User
Workspace
Session
Node
Resource
Process
Volume
Snapshot
Job
```

---

## `packages/protocol`

Protocoles de communication entre les composants.

Exemple conceptuel :

```text
workspace.created
workspace.started
workspace.stopped
process.started
process.stdout
process.exited
file.changed
snapshot.created
node.connected
node.disconnected
```

Les composants doivent communiquer via des contrats explicites plutôt que dépendre directement de leurs implémentations internes.

---

## `packages/sdk`

SDK permettant à des développeurs externes d'interagir avec Personal Cloud.

À terme :

```ts
const workspace = await personalCloud.workspaces.create({
  name: "research",
  cpu: 4,
  memory: "16GB"
})
```

---

# `infra/`

Infrastructure de déploiement.

## `infra/docker`

Environnements Docker pour le développement et le déploiement initial.

## `infra/kubernetes`

Déploiement distribué pour les environnements nécessitant une orchestration plus importante.

## `infra/terraform`

Infrastructure as Code.

Objectif :

```text
Infrastructure
     ↓
Declarative configuration
     ↓
Reproducible environment
```

---

# `docs/`

La documentation technique du projet.

## `docs/architecture`

Documents concernant :

* architecture générale ;
* control plane ;
* data plane ;
* workspace lifecycle ;
* compute ;
* storage.

## `docs/protocols`

Spécifications des protocoles et contrats.

## `docs/security`

Documentation concernant :

* isolation ;
* authentication ;
* authorization ;
* secrets ;
* networking ;
* threat model ;
* sandboxing.

---

# Core Data Model

Le modèle initial est volontairement simple.

```text
User
 │
 └── Workspace
       │
       ├── Runtime
       ├── Storage
       ├── Network
       ├── Processes
       └── Resources
```

Un workspace peut ensuite évoluer vers :

```text
Workspace
│
├── Identity
├── Runtime
├── Compute
├── Storage
├── Network
├── Sessions
├── Jobs
├── Snapshots
└── AI Runtime
```

---

# Workspace Lifecycle

Un workspace possède un cycle de vie explicite.

```text
             ┌───────────┐
             │  CREATED  │
             └─────┬─────┘
                   │
                   ▼
             ┌───────────┐
             │  STARTING │
             └─────┬─────┘
                   │
                   ▼
             ┌───────────┐
        ┌───►│  RUNNING  │◄───┐
        │    └─────┬─────┘    │
        │          │          │
        │          ▼          │
        │    ┌───────────┐    │
        │    │ SLEEPING  │    │
        │    └─────┬─────┘    │
        │          │          │
        │          ▼          │
        │    ┌───────────┐    │
        └────│ STOPPED   │────┘
             └─────┬─────┘
                   │
                   ▼
              RESTORING
                   │
                   ▼
                RUNNING
```

L'état du workspace doit être indépendant de l'appareil utilisé pour y accéder.

---

# Hybrid Computing

À terme, Personal Cloud doit pouvoir utiliser plusieurs sources de compute.

```text
                 Personal Cloud
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
       Cloud GPU     Home PC      Laptop
          │            │            │
       NVIDIA        Local CPU     Local CPU
```

Un workload pourrait être exécuté :

* localement ;
* dans le cloud ;
* sur un GPU distant ;
* sur plusieurs nodes.

Le système doit abstraire cette complexité derrière le workspace.

---

# Security Model

La sécurité est une primitive fondamentale.

Personal Cloud manipule potentiellement :

* fichiers personnels ;
* credentials ;
* processus ;
* réseau ;
* code ;
* workloads non fiables ;
* ressources GPU.

Le système doit donc traiter notamment :

### Isolation

```text
Workspace A
     │
     X
Workspace B
```

Un workspace ne doit pas pouvoir accéder arbitrairement aux ressources d'un autre.

### Identity

Chaque action doit pouvoir être associée à une identité.

### Authorization

Les permissions doivent être explicites.

### Secrets

Les credentials ne doivent jamais être stockés directement dans le code ou les logs.

### Network isolation

Les workloads doivent être isolables au niveau réseau.

### Runtime isolation

L'exécution de workloads non fiables devra pouvoir utiliser des primitives d'isolation appropriées.

Les détails du threat model seront documentés dans :

```text
docs/security/
```

---

# Development Strategy

Personal Cloud est un projet ambitieux, mais son développement doit rester incrémental.

Nous ne chercherons pas à implémenter toute l'architecture immédiatement.

## Phase 1 — Local Computer

Objectif :

```text
Create Workspace
       ↓
Start Container
       ↓
Persistent Filesystem
       ↓
Web Terminal
```

---

## Phase 2 — Persistent Workspace

Ajouter :

* process manager ;
* filesystem API ;
* snapshots ;
* workspace lifecycle ;
* WebSocket streaming.

---

## Phase 3 — Remote Computer

Ajouter :

* authentication ;
* gateway ;
* CLI ;
* remote connections ;
* multiple devices.

---

## Phase 4 — Compute Fabric

Ajouter :

* compute nodes ;
* scheduler ;
* resource management ;
* GPU support.

---

## Phase 5 — AI Computer

Ajouter :

* AI runtime ;
* computer tools ;
* planner ;
* persistent memory.

---

## Phase 6 — Distributed Personal Cloud

Ajouter :

* multiple nodes ;
* advanced scheduling ;
* distributed storage ;
* stronger isolation ;
* Kubernetes;
* infrastructure automation.

---

# Design Philosophy

Personal Cloud suit quelques principes simples.

### Build the primitive, not the feature.

Nous cherchons à construire des primitives informatiques réutilisables plutôt qu'une collection de fonctionnalités isolées.

### Persistent by default.

L'environnement doit survivre au changement de device.

### Explicit contracts.

Les composants communiquent via des contrats et protocoles explicites.

### Local first, distributed later.

Le système doit pouvoir fonctionner sur une seule machine avant de devenir distribué.

### Infrastructure before interface.

Une belle interface ne compense pas un runtime fragile.

### AI as a system component.

L'IA doit pouvoir utiliser l'ordinateur comme une primitive programmable, plutôt que d'être simplement ajoutée sous forme de chatbot.

### Security is architecture.

La sécurité ne doit pas être une couche ajoutée à la fin.

---

# Technology Direction

Le stack initial visé est volontairement pragmatique.

| Layer             | Direction               |
| ----------------- | ----------------------- |
| Web               | Next.js / TypeScript    |
| Desktop           | TBD                     |
| CLI               | TypeScript / Go         |
| Services          | Go                      |
| API               | HTTP / WebSocket / gRPC |
| Database          | PostgreSQL              |
| Cache / Messaging | Redis / NATS            |
| Runtime           | Containers → MicroVMs   |
| Object Storage    | S3-compatible           |
| Infrastructure    | Docker → Kubernetes     |
| IaC               | Terraform               |
| AI                | Model/provider agnostic |

Ces choix pourront évoluer avec les besoins réels du système.

---

# Project Status

> **Early development — experimental infrastructure project**

> **Phase 1 (Local Computer) en cours** : contrôleur de workspaces, API HTTP + WebSocket, CLI `pc` et terminal web minimal sont implémentés et testés. Voir `docs/architecture/overview.md`.

> **Phase 2 (Persistent Workspace) en cours** : volumes persistants, snapshots à adressage par contenu, API files, processus avec logs et registre de nœuds sont implémentés et testés. Voir `docs/architecture/storage.md`.

Personal Cloud est actuellement au stade de construction de ses primitives fondamentales.

Certaines parties de l'architecture représentent la direction à long terme du projet et ne sont pas encore implémentées.

L'objectif est de construire progressivement un système réellement fonctionnel plutôt qu'une architecture théorique complète.

---

# Contributing

Les contributions sont les bienvenues.

Avant de proposer une modification importante, il est recommandé de consulter :

```text
docs/architecture/
docs/protocols/
docs/security/
```

Les contributions doivent privilégier :

* simplicité ;
* séparation des responsabilités ;
* interfaces explicites ;
* sécurité ;
* tests ;
* observabilité ;
* documentation.

---

# Roadmap

La roadmap sera guidée par les primitives suivantes :

```text
Workspace
    ↓
Runtime
    ↓
Persistence
    ↓
Remote Access
    ↓
Compute
    ↓
Distributed Infrastructure
    ↓
AI
```

Chaque étape doit fonctionner avant que la suivante devienne une dépendance critique.

---

# Long-Term Vision

La vision finale dépasse le simple cloud personnel.

Aujourd'hui :

```text
Person
  ↓
Laptop
```

Demain :

```text
Person
  ↓
Persistent Computing Environment
  ├── Laptop
  ├── Desktop
  ├── Phone
  ├── Cloud
  ├── GPU
  ├── AI
  └── Network
```

Le matériel devient interchangeable.

Le compute devient programmable.

L'environnement devient persistant.

Et l'IA peut agir directement à l'intérieur de cet environnement.

---

# Final Principle

> **Your computer is not the machine on your desk.**
>
> **Your computer is the computational environment that follows you.**

Personal Cloud cherche à construire cette couche.

---

## License

License: **TBD**
