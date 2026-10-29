# Backends d'exécution

Le data plane (`runtime/`) expose un comportement réel partout grâce
à des backends locaux, et délègue à Docker quand un démon est
disponible. Chaque paquet fournit aussi un faux (fake) en mémoire
pour les tests.

## Conteneurs (`runtime/container`)

Interface `ContainerBackend` : `Create`, `Start`, `Stop` (idempotent),
`Remove`, `Exec` (stdio en streaming), `Inspect`.

- `local` (défaut de la Phase 1, `container.DefaultBackend()`) :
  conteneurs simulés par des processus OS, redémarrables. Utilisé par
  `cmd/api`.
- `docker` : enveloppe autour de la CLI `docker`, utilisée
  uniquement si un démon répond (`DockerAvailable`). Les tests du
  cycle de vie Docker sont ignorés proprement sans démon.

## Processus (`runtime/process`)

Interface `ProcessManager` : `Start`, `Stop`, `Signal`, `Wait`,
`Logs` (streaming), `Inspect`.

- `local` : exécution via `os/exec`.
- `fake` (`FakeProcessManager`) : processus simulés pour les tests.

## Filesystem (`runtime/filesystem`)

Interface `FS` : `read`, `write`, `move`, `delete`, `stat`, `list`,
`watch`, `snapshot`, `restore`.

- `local` (`LocalFS`) : adossé à un répertoire racine.
- `mem` (`MemFS`) : en mémoire, pour les tests.

## Réseau (`runtime/network`)

- `FreePort` / `FreePorts` : allocation de ports libres.
- `PortMapping` / `Gateway` : souches d'interfaces pour la
  passerelle future (`services/gateway`).

## Règle de la Phase 1

Tout doit fonctionner sans démon Docker ni service externe :
l'API, la CLI et toute la suite de tests tournent avec les backends
locaux. Docker reste une optimisation du déploiement, pas un
prérequis.
