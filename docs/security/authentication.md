# Authentification : notes de menace

Ce document recense les menaces pesant sur l'authentification par
jetons de la Phase 3 (`services/auth`, passerelle, CLI `pc`) et les
contre-mesures en place. Le modèle général est décrit dans
`../architecture/remote-access.md`.

## Exigence de production : TLS partout

Les jetons sont des secrets porteurs : **quiconque lit un jeton sur le
réseau usurpe l'appareil**. Le binaire `cmd/api` ne sert aujourd'hui
que du HTTP en clair ; en production, il **doit** donc se trouver
derrière un terminateur TLS (reverse proxy, tunnel) :

- `POST /v1/auth/login` (le jeton voyage dans la réponse) ;
- chaque appel authentifié (le jeton voyage dans `Authorization`) ;
- le proxy des ports workspace (corps des services des utilisateurs).

Sans TLS, un attaquant passif sur le réseau capture des jetons valables
90 jours. Le développement local (`localhost`) est le seul usage
acceptable en HTTP clair.

## Stockage des jetons

| Secret | Emplacement | Protection |
| ------ | ----------- | ---------- |
| Jeton client | `~/.pc/token` (`PC_CONFIG_DIR` en tests) | fichier `0600`, dossier `0700` |
| Secret HMAC serveur | `-auth-secret` / `PC_AUTH_SECRET` / `<storage>/auth-secret` | généré : 32 octets aléatoires, `0600` |
| Enregistrements serveur | `<storage>/auth-tokens.json` | JSON versionné, réécriture atomique, `0600` |

Menaces couvertes : lecture par un autre utilisateur local (permissions
propriétaire seul), corruption à l'écriture (fichier temporaire +
`rename`), jetons de tests mêlés aux jetons réels (`PC_CONFIG_DIR`
isole les tests).

Menaces **non** couvertes : un attaquant avec accès root ou au compte
lit les secrets au repos (pas de chiffrement) ; la compromission du
secret HMAC permet de forger des jetons arbitraires — il doit être
sauvegardé comme un mot de passe et changé en cas de doute (tous les
jetons existants deviennent alors invalides).

## Expiration

Les jetons vivent 90 jours (`auth.DefaultTTL`) : un jeton volé a une
durée d'abus bornée, et un jeton expiré est rejeté même avec une
signature valide. Il n'y a ni renouvellement automatique ni liste de
grâce : l'appareil refait `pc login`. Conséquence assumée : un appareil
perdu reste authentifié jusqu'à révocation ou expiry — révoquer vite.
`POST /v1/auth/revoke` tue le jeton **présenté** : depuis un appareil
perdu c'est impossible, et il n'y a pas encore de révocation par
appareil ; en cas de perte, un administrateur doit retirer à la main
l'enregistrement du `device` dans `<storage>/auth-tokens.json` (ou
régénérer le secret HMAC, ce qui invalide tous les jetons).

## Révocation

`POST /v1/auth/revoke` (elle-même gardée) supprime l'enregistrement du
jeton présenté et répond `204` ; la validation exigeant un
enregistrement présent, toute réutilisation répond `401` immédiatement,
sur la passerelle comme sur l'API (même `auth.Service` partagé). La
révocation est persistée (fichier atomique) : elle survit au redémarrage
du serveur.

## Fuites et oracles

- Les erreurs ne contiennent jamais de secret : `401` uniforme («
  authentication required ») que le jeton soit absent, malformé, inconnu
  ou expiré — un attaquant ne peut pas distinguer ces cas.
- Les journaux du serveur ne contiennent pas de jeton (vérifié en bout
  en bout : aucun `pc1.` dans les logs).
- Le proxy des ports retire l'en-tête `Authorization` avant l'amont :
  un service de workspace compromis ne récolte pas le jeton du client ;
  sa réponse `502` ne révèle pas l'adresse du backend.
- La signature HMAC est vérifiée en temps constant
  (`subtle.ConstantTimeCompare`) : pas d'oracle temporel sur le MAC.

## Limites connues (Phase 3)

- Un seul utilisateur effectif par défaut (`local-user`) : pas de
  contrôle d'accès entre utilisateurs ni entre workspaces.
- `GET /healthz` est ouvert (sonde ops) : il ne doit jamais exposer de
  données (`{"status":"ok"}` uniquement).
- Le registre de la passerelle (cibles, redirections) est en mémoire :
  un redémarrage oublie les `ports register` (à rejouer).
- Pas de limitation de débit sur `login` : à ajouter avec le TLS en
  production.
