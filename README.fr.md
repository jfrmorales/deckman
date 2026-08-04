# deckman

*[Español](README.md) · [English](README.en.md) · **Français***

Gérez les jeux de votre Steam Deck depuis votre ordinateur : voir ce qui est
installé, envoyer des jeux Windows et des ROMs, changer les jaquettes, déplacer
les jeux entre le SSD interne et la microSD, et libérer de la place.

C'est **un seul exécutable**. Un double-clic et l'interface s'ouvre dans le
navigateur. Rien à installer, ni sur le PC ni sur la Deck.

- `deckman.exe` — Windows
- `deckman` — Linux (binaire seul ou **Flatpak**, voir plus bas)

**[Télécharger la dernière version](https://github.com/jfrmorales/deckman/releases/latest)**
— chaque version publie ses binaires dans *Releases*, avec un `SHA256SUMS` pour
les vérifier :

```sh
sha256sum -c SHA256SUMS
chmod +x deckman-*-linux-amd64
```

Depuis la 0.6.2, ce `SHA256SUMS` est **signé**. Le `sha256sum` ci-dessus dit que
le téléchargement n'est pas corrompu ; la signature dit en plus que c'est bien
moi qui l'ai publié, et pas quelqu'un qui aurait mis la main sur l'accès aux
*Releases* :

```sh
cosign verify-blob --key cosign.pub --bundle SHA256SUMS.bundle SHA256SUMS
```

Ça doit afficher `Verified OK`. `cosign.pub` et `SHA256SUMS.bundle` sont dans la
même release ; la clé publique est toujours la même, donc si vous la gardez la
première fois, vous verrez si elle change un jour. La signature est aussi
inscrite dans le journal de transparence public de Sigstore, ce qui l'horodate.

Il y a aussi un `deckman-<version>.cdx.json` : l'inventaire de ce que contient
le binaire, au cas où il faudrait un jour savoir si une alerte de sécurité vous
concerne.

L'interface parle **espagnol, anglais et français**. Elle suit votre navigateur
par défaut, et un sélecteur se trouve dans la barre du haut.

---

## Ce qu'il fait

| | |
|---|---|
| **Bibliothèque** | Tous les jeux, Steam et hors Steam, avec leur jaquette, leur taille réelle, où ils se trouvent et ce qu'occupent le préfixe Proton et le cache de shaders. Un clic sur la jaquette ouvre la galerie. |
| **Envoyer un jeu** | Copie le dossier d'un jeu Windows vers `~/Games` et l'enregistre dans Steam, avec la version de Proton de votre choix. |
| **Détection automatique** | Au choix du dossier, il devine de quel jeu il s'agit, quel `.exe` le lance et quel Proton convient. |
| **Jaquettes** | Une galerie SteamGridDB pour choisir jaquette, image de fond, logo et icône de n'importe quel jeu, avec aperçu. Appliqué immédiatement. |
| **Émulation** | Copie les ROMs dans le dossier du système EmuDeck correspondant ; liste ce que contient déjà chaque système — seulement ceux qui ont vraiment des ROMs, pas les 181 dossiers d'EmuDeck — pour renommer ou supprimer ; et télécharge des ROMs par URL (c'est la Deck qui télécharge, pas le PC) avec une recherche archive.org limitée au système choisi. |
| **Jaquettes de ROMs** | Cherche la jaquette, l'écran-titre et une capture de chaque ROM sur libretro-thumbnails et les dépose là où ES-DE les cherche. Sans clé ni inscription. |
| **Déplacer** | Déplace un jeu entre le disque interne et la microSD sans le retélécharger. Les jeux hors Steam aussi : le dossier suit et le raccourci est mis à jour. |
| **Nettoyer** | Désinstalle des jeux et supprime séparément le préfixe Proton ou le cache de shaders. |

---

## Avant de commencer

Sur la Deck, une seule fois :

1. Mode bureau.
2. **Paramètres système → Bureau → Partage → SSH**, activez-le.
   (Ou dans un terminal : `sudo systemctl enable --now sshd`.)
3. Si vous n'avez jamais défini de mot de passe pour l'utilisateur `deck`,
   faites-le avec `passwd`. SteamOS n'en fournit pas et SSH refuse l'accès sans
   lui.

## Utilisation

Lancez le binaire. L'interface s'ouvre **dans sa propre fenêtre**, comme
n'importe quelle application de bureau :

- **Windows** : fenêtre native (WebView2, le moteur d'Edge, livré avec
  Windows 10 et 11). Fermer la fenêtre quitte deckman.
- **Linux** : une fenêtre d'application du navigateur Chromium présent (Chrome,
  Chromium, Brave, Edge, Vivaldi ; installations Flatpak comprises). Fermer la
  fenêtre quitte deckman — sauf si un transfert est en cours, qui se termine en
  arrière-plan avant l'arrêt.
- Si rien de tout cela n'est disponible, il s'ouvre dans un onglet du
  navigateur habituel (`http://127.0.0.1:8777`) et deckman se ferme avec le
  bouton **Quitter**.

Le premier écran est celui de la connexion : l'IP de la Deck, le mot de passe,
et sous **Options avancées** l'utilisateur, le port SSH s'ils ne sont pas ceux
par défaut (`deck` et `22`) et un nom pour distinguer les Decks. Cet écran
explique aussi comment activer SSH la première fois.

**Il n'y a aucun mot de passe prédéfini.** `deck` est le nom d'utilisateur
habituel de SteamOS, pas un mot de passe. À la connexion, deckman génère sa
propre clé SSH et la laisse installée sur la Deck : c'est pourquoi il ne
redemande plus rien ensuite. Le mot de passe n'est **enregistré nulle part**.
La clé apparaît comme `deckman@<votre-pc>` dans le `~/.ssh/authorized_keys` de
la Deck.

### Plusieurs Decks

Les Decks auxquelles vous vous connectez sont mémorisées et apparaissent sur
l'écran de connexion : un clic pour entrer dans l'une d'elles, et **+ Ajouter
une autre Deck** pour en enregistrer une nouvelle. Elles partagent toutes la
même clé SSH, qui identifie ce PC.

**Oublier** fait ce qu'il dit : en plus de la retirer de la liste, il **enlève
la clé SSH de cette Deck**, donc ce PC perd l'accès. Si la Deck est éteinte, on
ne peut pas la retirer sur le moment, et deckman le dit au lieu de se taire —
il vous indique quelle ligne supprimer à la main. Oublier la dernière Deck
supprime aussi la clé de ce PC.

```
deckman -port 9000      # un autre port
deckman -browser        # onglet du navigateur au lieu d'une fenêtre propre
deckman -no-browser     # ne pas ouvrir l'interface (serveur seul)
deckman -version        # (sous Windows n'affiche rien : le .exe n'a pas de console)
```

Si deckman est ouvert deux fois, le second détecte le premier et ouvre une
autre fenêtre vers lui, sans dupliquer le serveur.

La configuration est enregistrée dans `~/.config/deckman` (Linux) ou
`%AppData%\deckman` (Windows).

Pour quitter, fermer la fenêtre suffit ; le bouton **Quitter** de la barre du
haut fait la même chose et c'est la seule voie quand l'interface est dans un
onglet.

---

## Flatpak (Linux)

Sous Linux, deckman peut aussi s'installer en Flatpak, avec une icône dans le
menu des applications. Récupérez le `.flatpak` de
**[la dernière version](https://github.com/jfrmorales/deckman/releases/latest)**
puis :

```sh
flatpak install deckman-0.2.3.flatpak
```

À l'installation, flatpak prévient que l'origine n'est pas signée : c'est
normal pour un fichier téléchargé à la main. Le `SHA256SUMS` de la release est
là pour vérifier que c'est le bon.

Si vous préférez le compiler vous-même (podman ou docker nécessaire) :

```sh
make flatpak
```

Ce script compile le binaire (avec `./build.sh`, en conteneur), construit le
paquet avec `org.flatpak.Builder` (lui-même un Flatpak : rien n'est installé
sur le système) et le laisse installé pour l'utilisateur. Ensuite :

```sh
flatpak run io.github.jfrmorales.deckman     # ou depuis le menu : deckman
```

Détails du bac à sable :

- **Config** : va dans
  `~/.var/app/io.github.jfrmorales.deckman/config/deckman`. La première fois,
  si `~/.config/deckman` existe (du binaire seul), elle est recopiée : la
  connexion, la clé SSH et la clé SteamGridDB sont conservées.
- **Disques** : l'explorateur voit votre dossier personnel, `/run/media`,
  `/media` et `/mnt` en lecture seule. Si vos jeux sont ailleurs, donnez-lui
  accès avec
  `flatpak override --user --filesystem=/votre/chemin:ro io.github.jfrmorales.deckman`.
- **Fenêtre** : ouverte par le navigateur Chromium de l'hôte via
  `flatpak-spawn` (permission `--talk-name=org.freedesktop.Flatpak` du
  manifeste). Sans navigateur compatible, il retombe sur un onglet via le
  portail.
- S'il est lancé deux fois, la seconde instance détecte la première et pointe
  le navigateur vers elle au lieu de démarrer un autre serveur.

---

## Envoyer un jeu

Vous choisissez le dossier et deckman essaie de deviner le reste tout seul :

1. **L'identifiant Steam, depuis le dossier lui-même.** Beaucoup de repacks
   laissent un `steam_appid.txt` ou un `steam_emu.ini` avec le vrai app id.
   C'est exact et instantané, et cela ne dépend pas de deviner le bon nom.
2. **Sinon, par le nom.** Il nettoie celui du dossier
   (`Resident.Evil.4.REPACK-FitGirl` → `Resident Evil 4`) et le cherche dans la
   boutique Steam. En cas d'erreur, vous choisissez un autre résultat.
3. **L'exécutable.** Il classe les `.exe` par taille, ressemblance avec le nom
   et emplacement, et écarte le superflu : désinstalleurs, `vc_redist`,
   rapports d'erreur, trainers, sélecteurs de langue. Cela demande du soin :
   dans Resident Evil 4, le deuxième plus gros `.exe` est `CrashReport.exe`,
   avec 151 Mo.
4. **La version de Proton**, d'après ce que rapporte ProtonDB. *Platinum* et
   *gold* vont vers Proton Experimental ; *silver*, *bronze* et *borked* vers
   GE-Proton si vous l'avez installé.
5. **Les jaquettes**, si vous avez configuré SteamGridDB.

Tout cela est une proposition : les champs sont remplis mais modifiables. Si un
service ne répond pas, l'envoi se poursuit quand même.

**Les transferts peuvent reprendre** : si une copie est interrompue, relancez-la
et elle saute ce qui est déjà là.

---

## Jaquettes

Elles viennent de **SteamGridDB**, qui demande une clé gratuite : allez sur
`steamgriddb.com` → votre profil → *Preferences* → *API*, et collez-la dans
**Envoyer un jeu → Réglages des jaquettes**. Elle n'est enregistrée que sur ce
PC. Sans elle, tout le reste fonctionne pareil, mais le jeu apparaît avec un
cadre gris.

Deux façons de l'utiliser :

- **Automatique** à l'envoi d'un nouveau jeu : il prend la première de chaque
  type.
- **En choisissant vous-même** : cliquez sur la **jaquette** de n'importe
  quelle ligne de la bibliothèque. Des onglets pour jaquette verticale, image
  large, fond, logo et icône, avec un point vert sur celles qui ont déjà une
  image. Un clic ouvre un **aperçu** en grand avec les dimensions, l'auteur et
  le poids ; de là on applique ou on annule.

La miniature de la bibliothèque est celle qu'affiche Steam : d'abord le visuel
que vous avez choisi, sinon la jaquette de la boutique. Un jeu hors Steam sans
visuel affiche son initiale dans un cadre.

Cela marche avec **n'importe quel** jeu, Steam ou non. Pour ceux de Steam la
correspondance est exacte ; pour les autres la recherche se fait par nom et
vous pouvez la corriger avec la liste déroulante en haut à droite.

Attention aux titres répétés : « Resident Evil 4 », ce sont deux jeux
différents sur SteamGridDB, celui de 2005 et le remake de 2023, et la recherche
renvoie l'ancien en premier. Si vous voyez peu de visuels ou aucun animé,
regardez cette liste.

**Animées** : la case *Inclure les animées* les ramène (jaquettes et fonds ; il
n'y en a pas pour les logos et les icônes) et elles arrivent en premier car
elles sont minoritaires. Elles sont lourdes : un fond animé en 3840×1240 pèse
environ 45 Mo.

**Elles apparaissent immédiatement, sans redémarrer Steam.** Seule exception,
les icônes, que Steam ne peut pas changer à chaud.

---

## L'explorateur de dossiers

Comme le sélecteur du navigateur ne donne jamais le vrai chemin d'un dossier,
deckman apporte le sien. Il fonctionne comme celui de n'importe quel bureau :

- **Barre latérale** avec le dossier personnel, Téléchargements, Bureau,
  Documents, le dernier utilisé et les disques externes réellement montés.
- **Fil d'Ariane** cliquable : on saute à n'importe quelle partie du chemin.
- **Saisir ou coller un chemin** avec le bouton ✎.
- **Filtre** pour les dossiers bien remplis.
- **Un clic sélectionne, un double clic entre.** Sans rien de coché, le bouton
  utilise le dossier où vous êtes.
- **Clavier** : ↑ ↓ pour se déplacer, Entrée pour entrer ou choisir, Retour
  arrière pour remonter, Échap pour fermer, et n'importe quelle lettre lance le
  filtre.

Au choix de l'exécutable, les `.exe` sont mis en évidence. Au choix d'un
dossier, les fichiers apparaissent en gris au lieu d'être cachés : on distingue
ainsi un dossier vide de celui que l'on cherchait.

---

## Bon à savoir

**La première connexion à une Deck mémorise sa clé SSH**, sans rien demander.
Ensuite, si cette adresse répond avec une autre clé, deckman **s'arrête** et
affiche les deux empreintes au lieu de se connecter. Réinstaller SteamOS change
cette clé de façon légitime : dans ce cas, acceptez l'avertissement et c'est
tout. Si vous n'avez rien fait de tel, n'acceptez pas — ce qui répond là n'est
peut-être pas votre Deck, et se connecter lui livrerait le mot de passe. Les
clés mémorisées vivent dans `known_hosts`, dans le dossier de configuration de
deckman ; oublier une Deck emporte la sienne.

**Ajouter des jeux avec Steam ouvert est sûr**, mais uniquement parce que cela
passe par l'API de Steam. Steam garde la liste des raccourcis **en mémoire** et
réécrit `shortcuts.vdf` en quittant : modifier ce fichier dans son dos pendant
que Steam tourne n'est pas seulement perdu, cela peut vous laisser avec moins
de jeux qu'avant. C'est pourquoi, si Steam est ouvert mais ne répond pas,
deckman **refuse de continuer** plutôt que de prendre le risque ; et avant
d'écrire ce fichier il vérifie qu'aucun jeu qu'il ne touchait pas ne disparaît.

**Steam doit être fermé pour déplacer ou désinstaller des jeux Steam.** Il
garde son état en mémoire et réécrit les manifestes en quittant : si les
fichiers sont déplacés avec Steam ouvert, il annule le changement et laisse le
jeu marqué comme non installé. deckman le vérifie et refuse, plutôt que de
laisser les choses à moitié faites.

**Déplacer un jeu hors Steam marche bien avec Steam ouvert**, car il n'y a pas
de manifeste en jeu : le dossier est copié sur l'autre disque et on demande à
Steam de pointer le raccourci vers le nouvel emplacement. Deux détails :

- Ce n'est proposé que pour les jeux situés dans un dossier `Games` (ceux
  qu'envoie deckman). Un raccourci vers un émulateur ou vers Heroic lance
  quelque chose qui vit ailleurs, et le déplacer n'arrangerait rien.
- **Le préfixe Proton n'est pas déplacé.** Steam le crée toujours dans la
  bibliothèque principale, donc l'emmener sur la microSD laisserait les
  sauvegardes là où Steam ne les cherche pas.

**Le bouton Redémarrer Steam** est dans la barre de la bibliothèque. En mode
jeu, Steam tourne sous `steam-launcher.service` : cette unité est redémarrée et
il revient tout seul en quelques secondes. En mode bureau, on ne peut que le
fermer ; il faut le rouvrir à la main. Dans les deux cas, tout jeu en cours est
fermé, d'où la demande de confirmation.

**Avant de toucher à un fichier de configuration de Steam, une copie est
laissée** à côté de l'original, avec l'extension `.deckman.bak`.

**Requêtes vers internet** : la détection utilise la boutique Steam, ProtonDB
et SteamGridDB, en n'envoyant que le nom ou l'app id du jeu. Rien d'autre ne
sort du PC. **SteamDB n'est pas utilisé** : il n'a pas d'API publique et ses
conditions n'autorisent pas le grattage.

---

## Compiler

Il faut podman ou docker. **Go n'est pas installé sur le système** :

```sh
make setup    # une fois : vérifie les prérequis et prépare le clone
make build    # les deux binaires dans dist/
```

`make` tout court liste tout ce qui est possible.

### Tests

```sh
make check    # locaux
make deck     # + intégration contre votre Steam Deck
make audit    # analyse statique et vulnérabilités connues
```

`make audit` est volontairement séparé de `make check` : il compare avec une
base de données qui évolue toute seule, donc il peut passer au rouge sans que
personne n'ait rien touché. C'est très bien pour être au courant — le CI le
lance à chaque push — mais ce serait une très mauvaise porte pour publier.

L'IP et le mot de passe vont dans `deck.local.env` (créé par `make setup` à
partir de l'exemple), avec la clé SteamGridDB si vous voulez tester les
jaquettes. Ce fichier **n'est pas versionné** — ce dépôt est public.

Ceux d'intégration **ne touchent pas la vraie configuration** : ils montent une
fausse arborescence Steam dans `~/deckman-selftest` sur la Deck et la
suppriment à la fin. Les rares qui doivent forcément toucher à Steam (visuels à
chaud) restaurent ce qui était là.

Les tests locaux utilisent des échantillons dans `testdata/`, non versionnés
car ils contiennent des identifiants du compte Steam. Pour les générer :

```sh
scripts/fetch-testdata.sh 192.168.1.50
```

Sans eux, ces tests se sautent d'eux-mêmes.

### Versions

Les changements sont notés dans **[CHANGELOG.md](CHANGELOG.md)** sous *No
publicado*, et publiés d'une seule commande :

```sh
make release V=0.2.0
```

Elle vérifie, déplace ce qui est en attente vers la nouvelle version, met à
jour le metainfo du Flatpak, crée le tag `v0.2.0`, le pousse vers tous les
dépôts distants en vérifiant qu'il arrive sur chacun, et réinstalle le Flatpak
— pour qu'il ne prenne pas de retard sur le code.

C'est réversible tant que cela peut l'être : si quelque chose échoue **avant**
la publication, le commit et le tag sont défaits et vous êtes comme avant. Si
c'était déjà parti vers un dépôt distant, l'historique publié n'est pas
réécrit ; on vous dit l'état de chacun et ce qui manque.

La version vient d'un seul endroit, le tag, et se consulte à tout moment :

```sh
deckman --version                                  # binaire seul
flatpak run io.github.jfrmorales.deckman --version # Flatpak
flatpak list --columns=application,version | grep deckman
```

Le dépôt vit à deux endroits maintenus synchronisés :
[GitHub](https://github.com/jfrmorales/deckman) et un Forgejo auto-hébergé.

---

## Documentation

- **[docs/ARQUITECTURA.md](docs/ARQUITECTURA.md)** — comment le code est
  organisé et pourquoi les décisions ont été prises (en espagnol).
- **[docs/HALLAZGOS-STEAM.md](docs/HALLAZGOS-STEAM.md)** — comment Steam
  fonctionne à l'intérieur. Rien de tout cela n'est documenté par Valve : cela
  a été trouvé à la main contre une vraie Deck, et ce sont les pièges qui ont
  fait échouer des choses (en espagnol).
- **[CHANGELOG.md](CHANGELOG.md)** — ce qui a changé à chaque version.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** — comment compiler, tester et envoyer
  des correctifs.

---

## Contribuer

Les bugs et les correctifs sont les bienvenus : lisez
**[CONTRIBUTING.md](CONTRIBUTING.md)**, qui explique comment compiler (podman
ou docker suffit), comment lancer les tests contre une vraie Deck et les deux
règles auxquelles on ne déroge pas, parce que y déroger supprime les jeux des
gens.

Les traductions sont les bienvenues aussi. Les catalogues sont
`internal/i18n/catalogo.go` (messages du serveur) et
`internal/server/web/i18n-catalogo.js` (interface). Dans les deux, **la clé est
le texte espagnol**, donc ajouter une langue revient à ajouter un bloc.

---

## Licence

**GPL-3.0-or-later** — voir [LICENSE](LICENSE). Vous pouvez l'utiliser,
l'étudier, le modifier et le redistribuer ; si vous distribuez une version
modifiée, elle doit venir avec le code source et sous cette même licence.
