# Accès distant (Phase 3)

Ce document décrit l'accès distant de la Phase 3 : comment un appareil
obtient un jeton, comment la passerelle protège l'API et le cycle de vie
des jetons. Le détail des routes passerelle est dans `gateway.md`, les
menaces dans `../security/authentication.md`.

## Modèle d'authentification

L'identité est le couple **utilisateur + appareil** (`services/auth`,
`Identity{user_id, device, token_id, expires_at}`) :

- chaque appareil possède son propre jeton porteur (`Bearer`), émis par
  `POST /v1/auth/login` (`device` obligatoire, `user_id` optionnel,
  `local-user` par défaut) ;
- le jeton est opaque et versionné :
  `pc1.<base64url(id)>.<base64url(HMAC-SHA256(secret, id)>` (identifiant
  de 16 octets tirés de `crypto/rand`, signature vérifiée en temps
  constant) ;
- le serveur garde un enregistrement par jeton (`auth-tokens.json`,
  JSON versionné, réécriture atomique) : la validation exige à la fois
  une signature valide **et** un enregistrement présent et non expiré ;
- l'identité validée voyage dans le contexte des requêtes
  (`auth.ContextWithIdentity`), jamais dans les réponses d'erreur (qui
  ne contiennent aucun secret).

## Chemin des données via la passerelle

`cmd/api` sert la passerelle par défaut (`-gateway`, activé), qui
enveloppe l'API dans le même processus (voir `gateway.md`) :

```text
pc login laptop ──► POST /v1/auth/login (ouvert) ──► 201 {token,…}
                                                         │ ~/.pc/token (0600)
                                                         ▼
pc workspace create ──► Gateway :8080 ──► Bearer [REDACTED] ? ──► API → contrôleur
                        │ 401 uniforme sans jeton
                        ├─► /ports|/connections : registre + proxy local
                        └─► reste (/terminal, /exec WS…) : délégué à l'API,
                            même connexion (pas de hop réseau)
```

Seules deux routes restent ouvertes : `POST /v1/auth/login` (elle émet
le jeton) et `GET /healthz` (sonde ops). Tout le reste — y compris la
page `/terminal` et le handshake WebSocket `/exec` — exige un jeton
valide et répond `401` uniforme sinon. La passerelle et l'API partagent
le même `auth.Service` : un jeton émis par `login` authentifie les deux
couches, et `revoke` le tue partout.

Côté proxy, le jeton `Authorization` du client est **retiré** avant
l'amont (il authentifie la passerelle, pas le service du workspace) ;
l'adresse du client suit dans `X-Forwarded-For`. Une cible morte répond
`502` en JSON, sans révéler l'adresse du backend.

## Cycle de vie d'un jeton

```text
login (device) ──► émission (TTL 90 j) ──► usage (Bearer) ──► révocation
                                                              ou expiry
```

1. **Émission** : `pc login [device]` échange le nom d'appareil contre
   un jeton (`201`) et le sauvegarde (`~/.pc/token`, `0600`, dossier
   `0700`, configurable via `PC_CONFIG_DIR`). Chaque appel produit un
   jeton distinct : les appareils ne partagent jamais un jeton.
2. **Usage** : la CLI envoie le jeton sur chaque appel (REST comme
   WebSocket exec, y compris via `--api`/`PC_API` distant) ; un `401`
   (handshake compris) affiche « run `pc login` ».
3. **Expiration** : durée de vie de 90 jours (`auth.DefaultTTL`) ; un
   jeton expiré est rejeté même avec une signature valide. Pas de
   renouvellement automatique : l'appareil se reconnecte.
4. **Révocation** : `POST /v1/auth/revoke` (gardée) supprime
   l'enregistrement du jeton présenté (`204`) ; toute réutilisation
   répond `401` aussitôt.

Côté serveur, le secret HMAC vient de `-auth-secret`, sinon
`PC_AUTH_SECRET`, sinon un fichier généré (`<storage>/auth-secret`,
32 octets aléatoires, `0600`) ; les enregistrements vivent dans
`<storage>/auth-tokens.json` (`0600`).

## CLI distante

```bash
pc login --api https://cloud.exemple.fr laptop   # jeton distant
pc workspace create recherche                    # via la passerelle
pc exec <id> -- ./serveur                        # WebSocket authentifié
pc ports register <id> 3000 127.0.0.1:4001       # expose le service
```

Le drapeau global `--api` (ou `PC_API`) vise un serveur distant pour
toutes les commandes, `login` comprise.
