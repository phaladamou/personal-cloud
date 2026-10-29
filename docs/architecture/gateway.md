# Passerelle (gateway)

Ce document décrit la passerelle de la Phase 3 (`services/gateway`) :
le point d'entrée réseau unique du cloud personnel, devant le serveur
API. Elle porte le routage, l'accès distant aux services des
workspaces et le suivi des connexions.

## Chemin des données

`cmd/api` sert la passerelle par défaut (`-gateway`, activé) ; la
passerelle enveloppe le serveur API dans le même processus :

```text
Client (CLI pc, navigateur, HTTP)
        │
        ▼
Gateway :8080 (services/gateway)
  1. Auth : Bearer [REDACTED] obligatoire, sauf login et healthz
  2. Espace /ports|/connections → traité ici (registre + proxy)
  3. Tout le reste → délégué au serveur API, même connexion
        │
        ▼
API (services/api) → contrôleur → backends locaux
```

La délégation est une simple chaîne de `http.Handler` : la page
`/terminal/{id}` et l'upgrade WebSocket `/exec` traversent la
passerelle sans hop réseau supplémentaire, sur la même connexion
(prouvé par `TestExecStreamsThroughGateway`). Avec `-gateway=false`,
le binaire sert l'API nue comme en Phase 2 (page `/terminal`
ouverte, pas de proxy).

## Règle d'authentification

Derrière la passerelle, **tout exige un jeton porteur** (`pc login`),
y compris la page `/terminal` et le handshake WebSocket `/exec`
(en-tête `Authorization`) :

```text
ouvert : POST /v1/auth/login   (délivre le jeton)
         GET  /healthz          (sonde ops, sans données)
gardé  : tout le reste → 401 uniforme sans jeton valide
```

La passerelle et l'API partagent le même `auth.Service` : un jeton
émis par `login` authentifie les deux couches, et `revoke` le tue
partout.

## Proxy des services workspace

Un workspace expose ses services HTTP via des redirections de ports :

```text
GET /v1/workspaces/{id}/ports/{port}/... ──► http://{cible}/...
```

Résolution, dans l'ordre : la redirection du port exact, sinon la
cible par défaut du workspace, sinon `404`. Le préfixe passerelle est
retiré, la requête et les paramètres survivent, le `Host` suit le
backend. Une cible morte (connexion refusée) répond `502` en JSON,
sans révéler l'adresse du backend.

Sécurité : le jeton `Authorization` du client est **retiré** avant
l'amont (il authentifie la passerelle, pas le service du workspace) ;
l'adresse du client est transmise via `X-Forwarded-For`.

## Registre (`Registry`)

En mémoire, sûr pour un usage concurrent :

- **cibles** : `workspaceID → host:port` (`SetTarget` / `Target`),
  cible par défaut utilisée par le proxy en repli ;
- **redirections** : `workspaceID + port → Forward{target, protocol}`
  (`Register` / `Forwards` / `Lookup` / `Remove`), `409` en cas de
  doublon, protocole `tcp` par défaut ;
- **connexions** : sessions clientes suivies (`Open` / `Close` /
  `Count` / `Total`), identifiants `conn-N`.

Un workspace inconnu liste vide (redirections comme connexions),
jamais d'erreur — comme les gestionnaires de stockage.

## Routes

```text
POST   /v1/workspaces/{id}/ports                 {"port","target","protocol"*} → 201
GET    /v1/workspaces/{id}/ports                 {"ports":[...]} (triés par port)
DELETE /v1/workspaces/{id}/ports/{port}          → 204 (404 si absent)
GET    /v1/workspaces/{id}/ports/{port}[/...]    proxy (404 sans redirection, 502 cible morte)
POST   /v1/workspaces/{id}/connections           → 201 {"connection_id","count"}
GET    /v1/workspaces/{id}/connections           → 200 {"count","connections"}
DELETE /v1/workspaces/{id}/connections/{conn}    → 204 (404 si absent)
```

## CLI

```bash
pc login --api https://cloud.exemple.fr laptop   # jeton distant, appareil nommé
pc ports register <id> 3000 127.0.0.1:4001      # expose le service :4001
pc ports list <id>                               # redirections du workspace
pc ports remove <id> 3000                       # retire la redirection
```

Le drapeau global `--api` (ou `PC_API`) vise un serveur distant pour
toutes les commandes, `login` comprise ; la sortie de `login` rappelle
l'appareil enregistré.
