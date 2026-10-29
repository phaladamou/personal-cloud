# Terminal web

Ce document décrit le terminal web minimal de la Phase 1 et ce qui est
prévu pour la Phase 2.

## Phase 1 : page minimale

L'API sert une page unique (`services/api/terminal.html`, embarquée via
`go:embed`) sur `GET /terminal/{id}` :

- `xterm.js` chargé via CDN pour le rendu du terminal ;
- la page lit l'identifiant du workspace dans l'URL et ouvre le endpoint
  WebSocket `GET /v1/workspaces/{id}/exec?argv=/bin/sh` (coquille
  configurable via le paramètre `?shell=...`) ;
- les frappes clavier sont envoyées sur la socket, la sortie (texte ou
  binaire) est affichée dans le terminal.

Limitations assumées de la Phase 1 :

- pas de pseudo-terminal (pas de redimensionnement ni de contrôle de
  jobs) ;
- pas d'authentification (l'API ignore encore le jeton `pc login`) ;
- pas de reconnexion automatique ni de persistance de session.

## Phase 2 : application Next.js

L'application web complète arrive en Phase 2 dans `apps/web` (Next.js) :

- gestion des workspaces (création, état, ressources) ;
- terminal avec xterm.js intégré à l'application (plus de page isolée) ;
- explorateur de fichiers, gestion des services et des sessions ;
- authentification réelle adossée à `services/auth`.

La route `/terminal/{id}` est donc transitoire : elle prouve le streaming
dès la Phase 1 et sera remplacée par le terminal de l'application Next.js.
