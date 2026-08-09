# Harness différentiel Jellyfin 10.11.11

Ce répertoire exécute un même manifeste HTTP contre l'oracle stable Jellyfin
10.11.11 et une instance Rivune. Il produit des observations reproductibles; il
ne certifie ni ne promet une compatibilité Jellyfin générale.

## Prérequis

- Bash, `curl`, `jq`, Go et `sha256sum` (ou `shasum`);
- Docker avec Compose v2;
- une instance Rivune déjà démarrée, avec un profil compatible et un film nommé
  `Rivune Demo` dans sa bibliothèque.

L'oracle est exactement
`jellyfin/jellyfin:10.11.11@sha256:aefb67e6a7ff1debdd154a78a7bbb780fd0c873d8639210a7f6a2016ad2b35db`.
Compose ne le publie que sur `127.0.0.1:18096`. Ses volumes de configuration et
de cache sont jetables; `run.sh` les supprime avec `docker compose down
--volumes` à la sortie. Le média synthétique est monté en lecture seule.

## Configuration privée

Depuis la racine du dépôt:

```sh
cp scripts/jellyfin-compat/targets.env.example scripts/jellyfin-compat/targets.env
chmod 600 scripts/jellyfin-compat/targets.env
```

Remplacer tous les exemples. `targets.env` est ignoré par Git et chargé comme du
Bash de confiance. `run.sh` refuse un lien symbolique et tout mode POSIX donnant
un droit au groupe ou aux autres. Les secrets restent dans l'environnement ou
des fichiers temporaires privés: ils ne sont ni placés dans les arguments, ni
écrits dans les snapshots, erreurs ou logs. Pour choisir un autre fichier privé,
définir `JFCOMPAT_ENV_FILE`; les mêmes contrôles s'appliquent.

Les secrets de manifeste `{{secret:name}}` sont résolus séparément par cible:
`JFCOMPAT_UPSTREAM_NAME` et `JFCOMPAT_RIVUNE_NAME`. Les captures, dont le jeton
d'accès, sont également isolées par cible. Une capture marquée `secret` est
expurgée avant écriture des artefacts.

## Exécution

```sh
scripts/jellyfin-compat/run.sh
```

Le script:

1. vérifie les cinq SHA-256 inscrits dans `NOTICE`, puis copie uniquement les
   médias synthétiques Rivune dans `work/media`;
2. démarre l'oracle épinglé et attend son healthcheck;
3. vérifie que l'oracle est neuf, termine les Startup APIs, crée la bibliothèque
   `movies`, s'authentifie et sonde la tâche `RefreshLibrary` jusqu'à une nouvelle
   exécution réussie (backoff borné, aucune durée de scan supposée);
4. valide le manifeste, exécute les deux cibles et compare leurs artefacts;
5. arrête l'oracle et détruit ses volumes, même en cas d'échec.

Le résultat va par défaut dans un répertoire horodaté sous `work/runs/`. Une
valeur privée `JFCOMPAT_OUT_DIR` peut choisir un autre chemin; un chemin existant
n'est jamais écrasé.

Les trois commandes du cœur peuvent aussi être lancées depuis `server/`:

```sh
go run ./cmd/jellyfin-compat validate -manifest ../scripts/jellyfin-compat/requests.json
go run ./cmd/jellyfin-compat run -manifest ../scripts/jellyfin-compat/requests.json -target upstream=http://127.0.0.1:18096 -target rivune=http://127.0.0.1:8080 -out ../scripts/jellyfin-compat/work/manual
go run ./cmd/jellyfin-compat compare -left ../scripts/jellyfin-compat/work/manual/upstream -right ../scripts/jellyfin-compat/work/manual/rivune -out ../scripts/jellyfin-compat/work/manual/diff
```

Pour ces commandes manuelles, exporter d'abord les quatre variables privées du
fichier d'environnement et démarrer/bootstrapper l'oracle. Éviter d'inscrire des
secrets dans l'historique du shell.

## Portée des observations

Le manifeste couvre ping, informations système publiques, endpoint réseau,
authentification, utilisateur courant, vues, items, recherche, `HEAD` artwork,
`PlaybackInfo` et logout. Chaque étape borne statut, type ou taille de réponse.
Les identifiants, jetons, sessions, chemins, URL de lecture et horodatages sont
capturés ou canonisés avec une justification locale.

`compare: per-target` est volontaire lorsque les identités, bibliothèques,
providers de métadonnées ou topologies diffèrent. Les cas `observed-gap`
explicitent notamment la détection réseau et la disponibilité de l'artwork. Ces
snapshots restent inspectables, mais leur différence n'est pas transformée en
fausse équivalence. Seul le contrat sans contenu du logout est comparé
exactement.

Le bootstrap ne configure que l'oracle. Rivune doit donc déjà exposer un profil
et le titre synthétique attendus; le harness ne crée aucun compte natif Rivune et
ne modifie pas sa bibliothèque. Les différences de transcodage, de provider et
de codecs restent dépendantes du déploiement.

## Provenance et licences

Les MP4, VTT et SVG viennent exclusivement de
`server/internal/demo/assets`. Leur création, copyright, licence Apache-2.0 et
SHA-256 sont documentés dans le `NOTICE` racine. Le harness ne télécharge aucun
média et ne copie aucun code Jellyfin. Docker peut tirer l'image oracle épinglée
si elle n'est pas déjà locale; Jellyfin reste un logiciel GPL-2.0-or-later séparé
exécuté uniquement comme oracle. Les scripts et le manifeste originaux de ce
dépôt restent sous Apache-2.0.
