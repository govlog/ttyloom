# Manuel TTYloom

> 🇬🇧 English README with screenshots: [README.md](../README.md)

# Introduction

Le but de cette documentation est de présenter l'installation et la configuration de **ttyloom** sur **Debian 12** ou toute distribution Linux disposant de **Go 1.26.7**.

**ttyloom**[^1] est un client **Telegram et Discord** en mode texte, dans l'esprit d'**ircii** et de **BitchX** : une fenêtre de messages, une barre de statut, une ligne de commande. Il exploite le protocole graphique **kitty**[^2] pour afficher photos, stickers et GIF en ligne dans **Ghostty** ou **kitty**, se replie en demi-blocs Unicode ailleurs, souligne les liens (OSC 8), respecte la mise en forme des messages et charge les thèmes de couleurs de **Ghostty**.

> [!note]
> Compilé et testé (*go test*) sur **Ubuntu**, **Go 1.26.7**, **Ghostty 1.x**, le 2026-08-29. Validé sur un compte réel le 2026-08-29.

> [!note]
> Un second réseau, **Discord**, peut être connecté en parallèle : voir *Connexion à Discord*. Les deux comptes vivent dans la même interface, le panneau et la vue agrégée les mélangent, et */net* filtre sur l'un ou l'autre.

> [!note]
> Deux modes de connexion existent : **compte utilisateur** (téléphone, code, mot de passe 2FA) et **bot** (token BotFather). Le mode compte utilisateur est le mode principal ; le mode bot ne voit que les messages reçus après sa connexion.

# Prérequis

- Installer **Go** 1.26.7 ou supérieur : [go.dev/dl](https://go.dev/dl/)

- Installer le paquet *ffmpeg* (optionnel, nécessaire aux vidéos et aux WebP animés ; les GIF sont décodés en Go)

```bash
apt update ; apt install ffmpeg
```

- Installer *libhunspell-dev* et les dictionnaires (optionnel, nécessaire à la correction orthographique */set spell* ; compiler avec `-tags nospell` pour s'en passer entièrement)

```bash
apt update ; apt install libhunspell-dev hunspell-fr hunspell-en-us
```

- Créer une application sur [my.telegram.org](https://my.telegram.org/apps) pour obtenir *api_id* et *api_hash*

> [!note]
> Les identifiants *api_id* et *api_hash* sont obligatoires dans les deux modes de connexion : ils identifient l'application auprès de l'API MTProto, pas l'utilisateur.

# Installation

## Compilation

- Compiler le binaire **ttyloom**

```bash
cd ~/dev/perso/ttyloom
go build -o ttyloom ./cmd/ttyloom
```

- Installer le binaire dans **/usr/local/bin**

```bash
install -m 0755 ttyloom /usr/local/bin/ttyloom
```

- Vérifier que le binaire se lance et crée sa configuration

```bash
ttyloom ; ls -la ~/.config/ttyloom/
```

Sans réseau configuré, le résultat attendu est un message qui demande de configurer Telegram ou Discord et un fichier **~/.config/ttyloom/config.toml** créé avec les valeurs par défaut.

# Configuration

## Fichier de configuration

> [!note]
> Le fichier de configuration principal par défaut est **~/.config/ttyloom/config.toml**. Il est créé au premier lancement. Le répertoire peut être déplacé avec la variable d'environnement *TTYLOOM_DIR*.

- Remplacer le fichier **~/.config/ttyloom/config.toml** (adapter selon usage)

```bash
cat > ~/.config/ttyloom/config.toml <<\EOF
api_id = 0                          # https://my.telegram.org
api_hash = ""
bot_token = ""                      # vide = compte utilisateur ; sinon token BotFather (mode bot)
theme = ""                          # vide = theme Ghostty courant, sinon nom de theme
lang = ""                           # langue de l'interface : vide = selon $LANG, sinon fr, en, ou une chaîne fr+en (ordre de repli)
download_dir = "~/Downloads/ttyloom"
auto_media_max_kb = 5120
images = "auto"                     # auto | kitty | halfblock | off (F4)
images_hover = false                # image affichee seulement au survol du message (F5)
kitty_images = 48                   # images gardees dans le terminal (LRU)
video_inline_frames = 300           # images decodees pour une video lue en ligne (10 par seconde)
video = "show"                      # video en ligne : show | hidden (etiquette seule) | autoplay
link_previews = true                # apercu des liens : titre, description, vignette
maps = false                        # carte OpenStreetMap sous une position partagee (reseau vers tile.openstreetmap.org)
avatars = true                      # photos de profil devant les pseudos (kitty)
hover = "menu"                      # survol souris : menu (surbrillance + ligne d'aide) | highlight | off
separator = true                    # ligne entre les messages et la barre de statut
redline = true                      # ligne rouge « non lus » sous le dernier message lu
sidebar_sort = "recent"             # panneau : recent | alpha | unread (F7)
spell = "off"                       # correction de la saisie : off, ou un dico hunspell installé (fr, us, en_GB…), combinable avec + (Ctrl+R)
spell_quotes = false                # true : vérifie aussi les citations > et les blocs ```
sidebar_width = 26                  # largeur du panneau (glisser sa barre a la souris)
timestamps = true
timestamps_seconds = false          # horodatage avec les secondes (15:04:05)
cycle_mode = "next"                 # Ctrl+X : next | last_unread (fenêtres non lues l'une après l'autre, puis retour)
multiline = false                   # Maj+Entrée ouvre une zone de saisie étendue
bell = true                         # cloche sur message privé ou mention
auto_open_days = 7                  # fenetres ouvertes au demarrage pour les chats actifs (0 = jamais)
aggregate = false                   # fenetre 0 agregee (F6)
cache = true                        # historique et conversations sur disque
cache_messages = 2000               # messages gardes par conversation sur disque
notify = "terminal"                 # terminal (OSC 777 Ghostty/kitty) | desktop (notify-send) | off
log = false                         # journaliser toutes les nouvelles fenetres (/log par fenetre)
log_dir = "~/.local/share/ttyloom/logs"
EOF
```

Détail des options :

| Clé | Rôle |
|---|---|
| *api_id*, *api_hash* | identifiants de l'application Telegram, obligatoires pour Telegram seulement |
| *bot_token* | vide pour un compte utilisateur, token *123456:ABC-DEF…* pour un bot |
| *lang* | langue de l'interface : une langue embarquée (*fr*, *en*) ou une chaîne *fr+en* (ordre de repli, la première langue décide aussi du pluriel) ; vide suit *$LC_ALL*/*$LANG* (*fr_\** → français, sinon anglais) ; */set lang* change à chaud et refuse toute chaîne contenant un segment inconnu |
| *theme* | nom d'un thème Ghostty ; vide reprend la clé *theme* de **~/.config/ghostty/config**, *terminal* utilise les couleurs ANSI du terminal |
| *download_dir* | répertoire des médias téléchargés, nommés *AAAAMMJJ-HHMMSS_reseau_chatID_titre_messageID.ext* |
| *auto_media_max_kb* | taille maximale d'un média téléchargé automatiquement (0 à 524288) |
| *images* | *auto* détecte le protocole kitty, *halfblock* force les demi-blocs, *off* désactive les aperçus |
| *images_hover* | *true* n'insère plus les images dans le fil : elles apparaissent en surimpression au survol du message (*F5* bascule) |
| *kitty_images* | nombre d'images (photos, GIF, avatars) gardées transmises dans le terminal ; au-delà, la plus ancienne est libérée et retransmise à sa prochaine apparition |
| *video_inline_frames* | nombre d'images décodées quand une vidéo est lue dans le fil (*l*), 10 par seconde ; plafond de 200 Mo par vidéo |
| *video* | vidéos dans le fil : *show* garde la première image et la touche *l*, *hidden* n'affiche que l'étiquette (l'aperçu *v* et *o* restent disponibles), *autoplay* lance la lecture en boucle dès qu'une vidéo téléchargée s'affiche à l'écran (*s* arrête, *l* reprend la main) ; *video_inline_frames* borne le décodage dans les trois cas |
| *link_previews* | sous un message contenant un lien reconnu par Telegram, bloc *│* avec le site, le titre, la description et la vignette ; *o* ou un clic sur l'étiquette ouvre la page |
| *maps* | *true* télécharge une carte OpenStreetMap (4 tuiles, zoom 15, repère rouge) sous une position ou un lieu partagé et l'affiche comme une photo ; mise en cache dans *download_dir/maps* ; désactivé par défaut car chaque position provoque des requêtes vers *tile.openstreetmap.org* |
| *avatars* | photos de profil (2 cellules) devant les pseudos et dans le panneau, en protocole kitty seulement |
| *hover* | survol à la souris : *menu* surligne le message sous le pointeur et montre sa ligne d'aide, *highlight* surligne seulement (la ligne d'aide apparaît au clic), *off* ne fait rien au survol ; *true*/*false* restent acceptés ; règle aussi le suivi du pointeur (zone survolée éclairée, molette du panneau) |
| *separator* | ligne *─* entre la zone des messages et la barre de statut |
| *redline* | ligne rouge *─── ↑ non lus ───* placée après le dernier message lu quand une fenêtre est affichée (elle reste en place tant qu'on ne quitte pas la fenêtre, et se repositionne au retour ou après une absence) ; *false* la retire |
| *sidebar_width* | largeur du panneau latéral en colonnes (12 à la moitié de l'écran) ; saisir la barre *│* à la souris la modifie et l'enregistre |
| *sidebar_sort* | ordre du panneau : *recent* (dernier message), *alpha* (titre), *unread* (non-lus d'abord) ; *F7* cycle |
| *spell* | correction orthographique de la saisie : *off*, ou tout code de dictionnaire présent dans */usr/share/hunspell* (paire *.aff*/*.dic*, ex. *fr_FR*), combinable avec *+* (*fr+en*) ; *us* (legacy) et les préfixes à deux lettres comme *fr* résolvent vers le dictionnaire correspondant (*en_US*, *fr_FR*…) — fautes en rouge, *Ctrl+R* les parcourt (*Entrée* corrige, *i* ignore, *a* ajoute au dico perso *spell.txt*, *Ctrl+R* saute, *Échap* quitte), clic droit sur un mot souligné pour le corriger seul |
| *spell_quotes* | *on* : la correction vérifie aussi les lignes citées avec *>* et les blocs *```* (défaut *off*) |
| *timestamps* | horodatage devant chaque message |
| *timestamps_seconds* | horodatage avec les secondes, *15:04:05* au lieu de *15:04* |
| *cycle_mode* | comportement de *Ctrl+X* : *next* (fenêtre suivante), *last_unread* (les fenêtres non lues l'une après l'autre dans l'ordre des numéros, puis retour à la fenêtre quittée ; sans non-lus, cycle classique) |
| *multiline* | *Maj+Entrée* ouvre une zone de saisie étendue (une ligne d'écran par ligne du brouillon) ; *Entrée* envoie et replie, *Échap* replie en gardant le brouillon ; éditer un message multiligne la rouvre |
| *bell* | cloche du terminal (*\a*) sur message privé ou mention dans une fenêtre non affichée |
| *auto_open_days* | au démarrage, une fenêtre cachée par conversation active depuis N jours ; *0* désactive |
| *aggregate* | la fenêtre 0 affiche tous les messages de toutes les fenêtres (*F6* bascule) |
| *cache* | conserve les conversations et l'historique de chaque chat sur disque ; *false* désactive tout cache |
| *cache_messages* | messages gardés par conversation sur disque (les plus récents) ; la mémoire d'une fenêtre suit la même limite |
| *notify* | notification sur message privé ou mention quand le terminal n'a pas le focus : *terminal* (natif Ghostty/kitty), *desktop* (*notify-send*) ou *off* |
| *log*, *log_dir* | journalisation en texte brut des fenêtres (*/log* par fenêtre, *log = true* pour toutes les nouvelles) |
| *[discord] token_cmd* | commande qui imprime le token Discord sur sa sortie standard (voir *Connexion à Discord*) ; section absente ou clé vide = Discord n'est pas connecté |

> [!note]
> Les variables d'environnement *TG_API_ID*, *TG_API_HASH* et *TG_BOT_TOKEN* prennent le pas sur le fichier. Les commandes */set* et */theme* réécrivent **config.toml**, mais les valeurs venues de *TG_API_ID*, *TG_API_HASH* et *TG_BOT_TOKEN* n'y sont jamais recopiées.

- Restreindre les droits du fichier

```bash
chmod 0600 ~/.config/ttyloom/config.toml
```

## Connexion avec un compte utilisateur

> [!note]
> Mode principal. La session est enregistrée dans **~/.config/ttyloom/session.json** (droits *0600*, non chiffrée) : les connexions suivantes ne redemandent rien.

- Laisser *bot_token* vide dans **~/.config/ttyloom/config.toml**

- Lancer **ttyloom** : un QR code s'affiche (image kitty, ou demi-blocs ailleurs) à scanner depuis l'application Telegram, *Réglages → Appareils → Connecter un appareil* ; il se renouvelle seul à l'expiration, et un mot de passe à deux facteurs est demandé ensuite si le compte en a un

- Sinon, taper le numéro de téléphone à l'invite pour suivre la connexion classique par code, puis répondre aux invites de la fenêtre 0

```
Numéro de téléphone (+33…) :
Code reçu :
Mot de passe 2FA :
```

- Vérifier la connexion dans la fenêtre 0

```
*** connecté : @mon_compte
*** 42 conversations (/chats)
```

> [!caution]
> Le fichier **session.json** donne un accès complet au compte. **Ne jamais** le copier sur une machine partagée ni le versionner.

## Connexion en mode bot

> [!warning]
> A faire uniquement pour piloter un bot créé avec **@BotFather**. Un bot ne peut ni lister ses conversations ni relire l'historique : Telegram refuse *messages.getDialogs* et *messages.getHistory* aux bots. **ttyloom** n'affiche alors que les messages reçus après sa connexion, et les commandes */chats* et */history* sont indisponibles.

- Créer un bot et récupérer son token auprès de **@BotFather**

- Définir *bot_token* dans **~/.config/ttyloom/config.toml** (adapter selon usage)

```toml
bot_token = "123456789:AAExempleDeTokenBotFather"
```

- Lancer **ttyloom**

- Vérifier la connexion dans la fenêtre 0

```
*** connecté : @mon_bot
*** mode bot : pas de liste de conversations ni d'historique, les messages arrivent au fil de l'eau ; /query @utilisateur ou /join @canal pour ouvrir une fenêtre
```

> [!note]
> La session d'un bot est enregistrée à part, dans **~/.config/ttyloom/session-bot.json** : passer d'un mode à l'autre ne mélange jamais les identités.

## Connexion à Discord

> [!caution]
> Discord interdit les clients utilisant un token utilisateur et peut suspendre le compte. Voir la [politique officielle](https://discord.com/safety/360044104071-Tips-against-spam-and-hacking) et le [guide des identifiants](authentication.fr.md#discord).

> [!note]
> Le token n'est jamais écrit dans **config.toml** : la clé *token_cmd* nomme une commande qui l'imprime sur sa sortie standard (*pass*, *gopass*, *secret-tool*…). Elle est exécutée sans shell — ni tube, ni redirection, ni variable, et découpée sur les blancs (un chemin avec une espace passe par un script) — avec un délai maximal de 30 secondes, et sa sortie n'apparaît ni dans les journaux ni dans la fenêtre 0.

- Obtenir et stocker votre token : [procédure détaillée](authentication.fr.md#discord). Telegram est facultatif ; une configuration Discord seule est prise en charge.

- Ajouter la section **[discord]** à la fin de **~/.config/ttyloom/config.toml** (adapter selon usage)

```bash
cat >> ~/.config/ttyloom/config.toml <<\EOF

[discord]
token_cmd = "pass show discord/token"   # commande qui imprime le token, jamais le token
EOF
```

> [!warning]
> Les sections TOML doivent rester en fin de fichier : une clé simple écrite après *[discord]* serait lue comme une clé de la section Discord.

- Lancer **ttyloom** : les deux réseaux se connectent en parallèle et chacun annonce son compte en fenêtre 0

```
*** connecté : @mon_compte
*** connecté : @mon_compte_discord
*** 128 conversations (/chats)
```

Le panneau (*F2*) mélange les deux réseaux ; hors sections, une lettre en exposant devant chaque titre rappelle le réseau, *ᵗ* pour Telegram, *ᵈ* pour Discord. Les messages privés portent le nom du correspondant, les salons de guilde le titre *Guilde / #salon*, et les salons d'une même guilde restent groupés quel que soit l'ordre de tri (*F7*), classés entre eux par nom de salon.

Le panneau se découpe en sections dès qu'il y a de quoi : une ligne d'en-tête *── discord ───[-]* par réseau, une par serveur Discord, les salons y étant alors listés sans le préfixe *Guilde /*. Cliquer l'en-tête (ou */fold*) plie la section : ses conversations disparaissent et l'en-tête reprend leurs non-lus et le marqueur *[·]* d'une fenêtre ouverte, en *[+]*. L'état est gardé dans **sidebar.toml**. Avec un seul réseau et aucune guilde, aucune section n'apparaît.

- Filtrer l'affichage sur un réseau

| Commande | Effet |
|---|---|
| */net* | cycle : tous les réseaux, puis chacun dans l'ordre des noms |
| *Shift+F2* | même cycle au clavier |
| */net discord* | ne garde que Discord dans le panneau et la vue agrégée |
| */net telegram* | ne garde que Telegram |
| */net all* | lève le filtre |

Le filtre ne ferme aucune fenêtre : il ne touche que le panneau — la liste des conversations comme celle des fenêtres, où seules celles liées au réseau choisi restent, la fenêtre 0 toujours — et la vue agrégée. En mode fenêtres, *F2* enchaîne les réseaux avant de masquer le panneau : fenêtres de tous les réseaux, puis de chacun dans l'ordre des noms, puis caché. Avec un seul réseau connecté, */net* se contente de le nommer.

Ce qui fonctionne en v1 :

- messages privés, groupes privés et salons texte des guildes (types *texte* et *annonces*), à plat dans le panneau
- envoi, réponse, édition et suppression de messages, mise en forme Markdown Discord traduite dans les deux sens
- historique (*PgUp*, */history*) et cache disque comme sur Telegram
- réactions (*r*, bascule) et survol *qui a réagi*
- indication de saisie (*est en train d'écrire*), marque de lecture, pièces jointes et images (envoi avec */send*, téléchargement, aperçu)
- boîte des participants (*F3*) : les membres d'un salon de guilde, avec un accent de couleur sur ceux qui sont *en ligne* ; en message privé, la boîte et la barre de statut donnent la présence du correspondant (*en ligne*, *inactif*, *ne pas déranger*)
- *supprimer la conversation* sur un message privé ; sur un groupe privé, elle revient à quitter le groupe, Discord ne fait pas la différence
- recherche : */search* dans un salon ou un message privé (la recherche de Discord, celle du client officiel), et la recherche globale (*Ctrl+F* deux fois) qui interroge chaque serveur puis les dix messages privés les plus récents, quinze secondes au plus
- sélecteur de GIF (*Ctrl+G*) : Tenor via Discord, le GIF part comme l'adresse de sa page, et un GIF Tenor reçu s'anime dans le fil

Ce qui manque en v1 :

- fils de discussion, forums, salons vocaux et catégories : ils ne sont pas listés
- */whois*, carnet de contacts et */query* d'un pseudo inconnu — Discord ne résout pas un nom en conversation
- accusés de lecture *✓✓* (*qui a lu*), stickers, cartes de position
- *partir du salon* et *signaler / bloquer* : les entrées n'apparaissent pas dans le menu contextuel d'une conversation Discord, ni *signaler / bloquer* sur un membre de la boîte des participants — plutôt qu'une confirmation suivie de rien. *fermer la conversation* reste proposée sur un message privé : elle ne ferme que la fenêtre, aucun réseau n'est sollicité. *supprimer la conversation* sur un salon de guilde reste muette, seule une ligne *WARN* du journal interne (*/debug*) en garde la trace
- balayage de l'historique à la connexion : Discord ne le fait pas. Une page de salon coûte deux lectures REST, et une passe sur toute la liste des conversations à chaque démarrage ressemblerait à un client automatisé ; l'historique d'un salon est chargé à son ouverture, et le cache disque garde ce qui a déjà été lu

Les autres actions indisponibles le disent dans la fenêtre plutôt que de rester sans réponse.

```
*** indisponible sur discord
```

## Thème

- Lister les thèmes disponibles depuis la ligne de commande de **ttyloom**

```
/theme list catppuccin
```

- Appliquer un thème (la touche Tab complète le nom)

```
/theme Catppuccin Mocha
```

Le thème est appliqué immédiatement et enregistré dans **config.toml**. Les fichiers sont lus dans **~/.config/ttyloom/themes**, **~/.config/ghostty/themes**, **$GHOSTTY_RESOURCES_DIR/themes** puis **/usr/share/ghostty/themes**, au format Ghostty (*palette = N=#rrggbb*, *background*, *foreground*).

- Choisir un thème dans un sélecteur : */theme* sans argument ouvre une liste filtrable (flèches, molette, clic), chaque déplacement applique le thème en direct, *Entrée* le conserve, *Échap* restaure l'ancien

# Utilisation

## Fenêtres

Par défaut, la fenêtre 0 est la fenêtre de statut : connexion, journaux, résultats de */chats*, */theme list* et */help*. Chaque conversation ouverte occupe une fenêtre numérotée.

| Commande ou touche | Effet |
|---|---|
| */window new* | crée une fenêtre et y bascule |
| */window new hide* | crée une fenêtre sans y aller |
| *Ctrl+X* | fenêtre suivante (cycle, revient à 0) ; avec */set cycle_mode last_unread*, les fenêtres non lues l'une après l'autre, puis retour à la fenêtre quittée |
| *Alt+1* … *Alt+9*, *Alt+0* | fenêtres 1 à 9, 0 |
| *Alt+←*, *Alt+→* | fenêtre précédente, suivante |
| */win N* ou */win nom* | bascule vers une fenêtre |
| */N* | va à la fenêtre N (*/5*, */21*), raccourci **ircii** de */window N* |
| */window close* | ferme la fenêtre courante (jamais la 0) |
| */window list* | liste les fenêtres et leur activité |
| *F2* | panneau latéral (conversations, fenêtres, caché) ; en mode fenêtres, cycle les réseaux |
| *Shift+F2* | cycle le filtre réseau, comme */net* sans argument |
| *F3* | boîte des participants en haut à droite |
| *F4* | mode d'affichage des images : *kitty*, *halfblock*, *off* |
| *F5* | image au survol seulement |
| *F6* | fenêtre 0 agrégée |
| *F7* | ordre du panneau, dans les deux modes : récents, a→z, non-lus |
| *Ctrl+R* | parcourt les fautes de la saisie (*/set spell*) : *Entrée* corrige, *i* ignore, *a* ajoute au dico, *Échap* quitte |

- Ouvrir un privé dans une nouvelle fenêtre, comme sous **ircii**

```
/window new hide
Ctrl+X
/query antonio
```

Un message entrant pour une conversation sans fenêtre crée une fenêtre cachée, signalée dans *[Act: …]* de la barre de statut.

## Conversations

| Commande | Effet |
|---|---|
| */query nom* (*/q*) | lie la fenêtre courante à un privé, nom exact ou préfixe |
| */join @canal* (*/j*) | rejoint un canal ou un groupe public et le lie |
| */msg nom texte* | envoie sans changer de fenêtre |
| */chats* | liste les conversations, non lues en surbrillance |
| */net [réseau]* | filtre le panneau et la vue agrégée sur un réseau (*telegram*, *discord*, *all*) ; sans argument, cycle, comme *Shift+F2* |
| */fold [section]* | plie ou déplie une section du panneau, comme un clic sur sa ligne d'en-tête ; la section se nomme par sa clé (*telegram*, *discord:Gophers*) ou par un préfixe du nom affiché ; sans argument, liste les sections et leur état (*[+]* pliée, *[-]* dépliée) |
| */history N* | charge N messages plus anciens (*PgUp* en haut de l'écran fait de même) |
| */clear* (*/c*) | vide la fenêtre |
| */rename [cible] nom*, */unrename [cible]* | renomme localement une conversation ou un contact (alias enregistré dans **aliases.toml**, appliqué au panneau, à la barre de statut, à l'agrégé, à la complétion et au pseudo du contact en privé ; */whois* garde le titre Telegram) |
| */help* (*/h*) | une ligne par commande, touche et option, groupées par section ; */help <sujet>* détaille une commande, une touche (*/help F3*) ou une clé (*/help hover*), *Tab* complète les sujets |
| texte sans */* | envoie au chat de la fenêtre courante |
| *//texte* | envoie un texte commençant par */* |

## Médias

| Commande | Effet |
|---|---|
| */open* | ouvre le dernier média de la fenêtre avec *xdg-open* |
| */open N* | ouvre le N-ième média depuis la fin |
| */set images halfblock* | bascule l'affichage en demi-blocs (*auto*, *kitty*, *off*) ; *F4* cycle |
| */view* ou *v* sur une sélection | aperçu plein écran du média, *Échap* ferme, *o* ouvre le fichier ; *+*/*-* ou la molette zooment (centré, jusqu'à 8×), les flèches déplacent la vue, *0* réajuste |
| *l* sur une vidéo téléchargée | lit la vidéo dans le fil (sans son), *l* de nouveau met en pause, *s* revient au début ; l'étiquette indique *décodage 42 %* le temps d'extraire les images |
| */set video hidden* | retire les vidéos du fil, étiquette seule ; *autoplay* les lance au contraire toutes seules, en boucle |
| */send chemin [légende]* | envoie un fichier local : photo pour png/jpeg, vidéo pour un mp4 (durée et dimensions relevées par *ffprobe*, un mp4 part comme vidéo lisible et non comme fichier), document sinon |
| *Ctrl+V* | colle une image du presse-papier (*wl-paste* ou *xclip*) et propose *(e)* envoyer, *(l)* légende, *(a)* annuler ; un texte est inséré dans la saisie |
| *Ctrl+G* ou */gif [recherche]* | sélecteur de GIF animés : les GIF tendance tout de suite, la frappe cherche (le bot *@gif* sur Telegram, Tenor via Discord), les flèches ou la molette déplacent, *Entrée* ou un clic envoie le GIF choisi dans la conversation, *Échap* ferme. Les aperçus visibles sont téléchargés dans le cache et animés (kitty, ou demi-blocs), 40 images chacun au plus |
| */set auto_media_max_kb 20480* | relève le seuil de téléchargement automatique |

Les photos, stickers et GIF sous le seuil sont téléchargés dans *download_dir* et affichés en ligne ; les GIF sont animés. Un lien reconnu par Telegram reçoit un aperçu (*link_previews*) : étiquette *[lien · site · titre]* cliquable, description et vignette. Seules les adresses *http*, *https* et *mailto* sont ouvertes avec *xdg-open* ; tout autre schéma est refusé. Un média plus lourd reste une étiquette *[video 00:42 · 38 Mo]* que */open* télécharge à la demande. Un document dont l'extension n'est pas sur la liste blanche (images, vidéos, audio, bureautique, archives, texte, *pdf*) est enregistré en *.bin* et *o* refuse de l'ouvrir ; une image dont les dimensions dépassent 40 mégapixels est refusée au décodage (un GIF animé est plafonné à environ 1,6 mégapixel par image). Les fichiers téléchargés sont créés en *0600* dans des répertoires *0700*.

En protocole kitty, une image n'est transmise au terminal qu'au moment où elle apparaît à l'écran, et au plus *kitty_images* images restent transmises à la fois (la plus anciennement affichée est libérée en premier). Chaque image de GIF est envoyée sur un second identifiant avant que la précédente ne soit effacée, ce qui évite le clignotement noir entre deux images. Les placements portent un identifiant stable : un simple redimensionnement ne retransmet rien, seul un changement de taille de police re-décode les images visibles.

Avec *images_hover* (*F5*), le fil ne réserve aucune ligne : l'image apparaît en surimpression quand la souris survole le message, à droite de l'étiquette si la place existe, sinon sous le bloc, jamais par-dessus le message lui-même.

## Cache et synchronisation

> [!note]
> Par défaut, le répertoire de cache est **~/.cache/ttyloom** (ou **$XDG_CACHE_HOME/ttyloom**) ; avec *TTYLOOM_DIR* défini, il devient **$TTYLOOM_DIR/cache**, de sorte que deux comptes ne partagent jamais un cache. Le cache d'un bot vit dans un sous-répertoire *bot*.

Au lancement, les conversations et l'historique du cache s'affichent avant même la connexion ; les fenêtres des conversations actives (*auto_open_days*) sont créées tout de suite, les autres historiques se chargent à l'ouverture de leur fenêtre. Remonter en haut d'une fenêtre (*PgUp* ou molette) charge la page précédente depuis Telegram, par tranches de 100 messages, jusqu'au début de la conversation ; ces pages rejoignent le cache, qui garde les *cache_messages* messages les plus récents de chaque conversation. Une fois connecté, **ttyloom** synchronise toutes les conversations l'une après l'autre (les 200 derniers messages, puis seulement les nouveautés aux démarrages suivants), à raison d'une requête toutes les 150 ms, sans jamais s'arrêter sur une limitation *FLOOD_WAIT*. La fenêtre 0 affiche la progression, *synchronisation 37/120 chats*, puis *synchronisation terminée*. Les médias sous le seuil sont téléchargés pour les conversations ouvertes.

- Vider le cache

```bash
rm -rf ~/.cache/ttyloom/history ~/.cache/ttyloom/dialogs.gob
```

> [!note]
> Un cache appartenant à un autre compte est détecté à la connexion et ignoré (*cache d'un autre compte ignoré* en fenêtre 0). En mode bot, aucune synchronisation n'a lieu : le cache ne garde que les messages reçus.

## Panneau latéral et notifications

- Afficher le panneau avec *F2* : une première pression liste toutes les conversations (épinglées en tête, non-lus en couleur), une deuxième liste les fenêtres ouvertes, une troisième le masque
- Avec deux réseaux connectés, la liste des fenêtres enchaîne les réseaux : chaque nouvelle pression de *F2* ne garde que les fenêtres d'un réseau, puis masque le panneau ; *Shift+F2* fait le même cycle sans quitter le mode courant

En mode fenêtres, les lignes suivent le même ordre de tri que le mode conversations (*F7*, */set sidebar_sort*) appliqué à la conversation de chaque fenêtre : la fenêtre 0 reste en tête, les fenêtres liées à aucune conversation ferment la marche dans l'ordre des numéros. Les numéros affichés restent les vrais numéros de fenêtre — seul leur ordre change, et le tri par défaut (*récents*) ne donne donc plus l'ordre des numéros.

Chaque ligne est préfixée de *#* pour un salon, *&* pour un canal, *@* pour un pseudo, *\** pour la fenêtre de statut ; la photo de profil précède le titre en kitty. Le titre de la conversation courante défile de droite à gauche s'il dépasse la colonne. Un en-tête coiffe le panneau : le titre du mode et l'ordre de tri entre crochets, dans les deux modes — cliquer la ligne de titre change l'ordre, comme *F7* (*récents*, *a→z*, *non-lus* ; */set sidebar_sort*).

Un clic sur une ligne du panneau ouvre ou rejoint la conversation ; un clic sur une ligne d'en-tête de section la plie ou la déplie (*/fold*) ; la molette au-dessus du panneau passe à la fenêtre précédente ou suivante dans l'ordre du panneau (en-têtes et conversations jamais ouvertes enjambés, aucun rebouclage aux extrémités) sans jamais en ouvrir une nouvelle — un clic reste nécessaire pour ouvrir ; une fenêtre jamais visitée (pré-ouverte par *auto_open_days*) charge sa première page d'historique à l'arrivée, comme par clic ou *Alt+N* ; atterrir sur une fenêtre à la molette la marque lue et rafraîchit la boîte des participants (*F3*) si elle est ouverte, comme un clic, *Alt+N* ou */win N*. La dernière ligne, *+ nouveau message* (ou */new*, *Ctrl+N*), ouvre une boîte de recherche : mes contacts et conversations filtrés en direct, puis, à partir de trois caractères ou d'un *@*, les résultats de Telegram dans une section *sur Telegram* ; *Entrée* ou un clic ouvre la conversation dans une fenêtre. Un clic droit ouvre un menu à l'endroit du clic : *partir du salon* (ou *fermer la conversation* pour un privé), *signaler / bloquer*, *supprimer la conversation*, *infos*, *rechercher* ; les actions irréversibles demandent une confirmation *(y/n)*. Le même menu existe sur un membre de la boîte des participants (*message privé*, *infos*, *signaler / bloquer*).

- Afficher les participants avec *F3* : une boîte en haut à droite liste le pair et sa présence en privé, les membres (admins *★* en tête, en ligne en couleur) en groupe, le nombre d'abonnés en canal ; un clic sur un membre ouvre sa conversation dans une fenêtre dédiée, la croix *[x]* de la bordure ferme la boîte

À droite de la zone des messages, une barre de défilement indique la position dans l'historique : un clic saute, un glisser suit la souris ; la piste s'éclaire en couleur d'accent dès que le pointeur est sur les messages ou sur la colonne, et le curseur s'épaissit en bloc plein (*█*) quand le pointeur est sur la colonne elle-même. La barre de statut résume l'activité des fenêtres non affichées sous la forme *[Act: 2(3),5(1)]* (numéro de fenêtre et nombre de messages). Une cloche retentit sur un message privé ou une mention de mon nom ailleurs que dans la fenêtre courante (*/set bell off* la coupe). Au démarrage, les conversations actives depuis *auto_open_days* jours reçoivent une fenêtre cachée, signalée dans *[Act]*.

## Fenêtre 0 agrégée et recherche

- Basculer la fenêtre 0 en vue agrégée avec *F6* (ou */set aggregate on*) : tous les messages de toutes les fenêtres y défilent, préfixés du nom de la conversation, *12:01 [alice] <alice> salut*, comme sous **BitchX**

Taper du texte dans la vue agrégée répond à la conversation du dernier message affiché (le prompt indique *[réponse à alice]*). Les messages affichés dans l'agrégé sont considérés lus : l'accusé de lecture part vers Telegram pour chaque conversation concernée.

- Chercher dans la fenêtre courante avec *Ctrl+F* : la frappe filtre en direct, sans tenir compte de la casse ni des accents, les occurrences sont surlignées et la barre de statut affiche *[recherche : 3/17]* ; *Entrée* remonte à l'occurrence précédente, *Ctrl+N* descend, *Échap* quitte

- Chercher sur tout le compte : *Ctrl+F* une seconde fois pendant la recherche locale interroge chaque réseau qui sait chercher (Telegram par *messages.searchGlobal*, Discord serveur par serveur puis dans les dix messages privés récents, quinze secondes au plus ; 50 résultats, 300 ms après la dernière frappe), ou seulement le réseau du filtre */net* (*F2* en mode fenêtres le fait tourner), fusionne les réponses du plus récent au plus ancien une fois toutes arrivées, et ouvre une boîte de résultats, une ligne par message (*[#salon|@pseudo]  date  <auteur>  extrait…*, occurrence en couleur) ; *↑*/*↓*, la molette ou un clic choisissent, *Entrée* ouvre la conversation et saute au message, *Ctrl+F* revient à la recherche locale, *Échap* ferme tout

## Brouillons, focus, journalisation

Chaque fenêtre garde son brouillon : le texte en cours de saisie survit à *Ctrl+X* et revient avec la fenêtre. */me <texte>* envoie une action en italique, comme sous **ircii**.

**ttyloom** sait si le terminal a le focus : tant qu'il ne l'a pas, la barre de statut affiche *[absent]*, aucun accusé de lecture ne part et les compteurs de non-lus montent, y compris dans la fenêtre courante ; au retour du focus, la fenêtre affichée est marquée lue. Les notifications (*notify*) et la cloche ne partent que pendant l'absence ou pour une fenêtre non affichée.

- Journaliser la fenêtre courante (bascule ; */log on* et */log off* forcent)

```
/log
```

Les lignes s'ajoutent à **<log_dir>/<titre>-<réseau>-<id>.log** au format *2026-08-30 12:01 <nick> texte* ; *[log]* apparaît dans la barre de statut. Le titre est coupé à 40 caractères, le reste du nom identifie la conversation : deux conversations aux titres longs et proches ne partagent plus un journal. Un renommage ouvre un nouveau fichier, et les journaux d'avant gardent leur ancien nom (aucune reprise automatique).

- Chercher dans tout l'historique Telegram d'une conversation (recherche serveur, 50 résultats)

```
/search facture
```

Les résultats s'ouvrent dans une fenêtre *?facture* liée à la même conversation : sélection, réponse, réactions et édition y fonctionnent ; *Entrée* sur un résultat sélectionné (ou *g*) saute au message dans la vraie fenêtre ; */close* la ferme.

- Afficher la fiche d'un contact (nom, @username, téléphone, bio, dernière connexion, discussions en commun)

```
/whois antonio
```

Sans argument, */whois* décrit la conversation privée courante. La barre de statut d'un privé affiche la présence, *[alice · en ligne]* ou *[alice · vu il y a 5 min]*.

- Afficher le journal interne à la place de la fenêtre 0 (bascule ; *F6* ou un changement de fenêtre y met fin)

```
/debug
```

Les avertissements de la bibliothèque Telegram (*WARN*) n'apparaissent que là, avec le statut *[0:debug]* ; les erreurs (*ERROR*) restent aussi affichées en fenêtre 0. Rien n'est écrit sur disque.

Les blocs de code reçus sont encadrés d'une barre *┃* et, à l'ouverture d'une conversation avec des non-lus, la vue se place sur la ligne rouge *─── ↑ non lus ───* (*redline*). Un indicateur « tape… » est envoyé pendant la saisie (au plus toutes les 5 s).

## Messages

- Sélectionner un message avec *Alt+↑* / *Alt+↓* ou d'un clic (hors lien et hors image) : le bloc passe en surbrillance avec une barre *▌* et une ligne d'aide, *👍 · e éditer · d supprimer · p répondre · r réagir · i info · o ouvrir · v voir · l lire · c copier · Esc*, dont chaque mot est cliquable ; un second clic sur le message le désélectionne

Le survol à la souris (*hover*) surligne le message sous le pointeur et affiche la même ligne d'aide sans le sélectionner, sans déplacer le texte. L'emoji en tête de la ligne d'aide est la réaction rapide (ma réaction s'il y en a une, sinon la plus populaire, sinon un emoji déduit du contenu) : un clic dessus l'ajoute ou la retire. Un double-clic sur un message ajoute ou retire *👍*.

La zone sous le pointeur prend aussi la molette : au-dessus du panneau, elle passe à la fenêtre précédente ou suivante (sans jamais en ouvrir une nouvelle) ; au-dessus des messages ou de l'ascenseur, elle fait défiler l'historique comme avant. La zone active se signale sans rien déplacer — barre *│* du panneau ou colonne de l'ascenseur en couleur d'accent, curseur *█* sur la colonne elle-même. *hover = off* coupe tout le suivi, molette comprise, et rend le comportement d'avant à l'octet près.

| Touche | Effet |
|---|---|
| *e* | édite mon message : la saisie prend le prompt *✎ #id ›* préremplie, *Entrée* envoie, *Échap* annule |
| *d* | supprime mon message pour tout le monde après confirmation *(y/n)* |
| *p* | répond au message : prompt *↩ #id ›*, la citation apparaît sous ma réponse |
| *r* | réagit : le sélecteur ne propose que les réactions acceptées par Telegram (et par le salon), un clic sur un emoji le choisit ; choisir de nouveau le même retire la réaction |
| *i* | affiche sous le message sa date d'envoi, d'édition, son état de lecture (*lu ✓✓*, en groupe *lu par : alice, bob*) ou, pour un message d'autrui, ses vues et son numéro ; *Échap* retire ces lignes |
| *o* | ouvre le média du message avec *xdg-open* |
| *v* | aperçu plein écran du média |
| *l*, *s* | lecture ou pause d'une vidéo dans le fil, retour au début |
| *c* | copie le texte du message dans le presse-papier (OSC 52) |
| *g* | va au message cité par une réponse (un clic sur la citation *│ …* fait de même), ou, dans une fenêtre de recherche, au message dans sa conversation ; l'historique manquant est chargé autour du message |
| *Échap* | désélectionne |

- Éditer mon dernier message sans le chercher : *↑* sur une saisie vide

Un clic sur une réaction sous n'importe quel message l'ajoute ou la retire, sans passer par la sélection ; la ligne des réactions se met à jour dès la réponse de Telegram. Mes messages portent en fin de première ligne *✓* (envoyé) puis *✓✓* (lu par le destinataire) ; les messages reçus portent *•* tant que je ne les ai pas lus, puis *✓✓*. *i* sur un message avec réactions indique qui a réagi (*réactions : 👍 alice, bob · ❤ moi*).

- Copier plusieurs messages : glisser à la souris du premier au dernier (la plage se surligne), le texte brut des messages, sans horodatage ni indentation, part dans le presse-papier au relâchement (*copié : 3 messages*)

> [!note]
> L'édition renvoie du texte brut : un message qui contenait du gras ou un bloc de code perd sa mise en forme. Mes réactions sont affichées en couleur d'accent. En groupe, *lu par* dépend des réglages de confidentialité de Telegram et affiche *indisponible* quand le serveur refuse.

## Ligne de saisie

| Touche | Effet |
|---|---|
| *←*, *→*, *Home*, *End*, *Ctrl+A*, *Ctrl+E* | déplacement (en zone étendue, *Home*/*End* et *Ctrl+A*/*Ctrl+E* jouent sur la ligne courante) |
| *Ctrl+←*, *Ctrl+→* | saute de mot en mot |
| *Ctrl+K*, *Ctrl+U*, *Ctrl+W* | supprime jusqu'à la fin, jusqu'au début, le mot précédent |
| *Ctrl+T* ou */emoji* | sélecteur d'emoji : la frappe filtre, les flèches ou un clic choisissent, *Entrée* insère, *Échap* ferme |
| *↑*, *↓* | historique de saisie |
| *Maj+Entrée* ou *Alt+Entrée* | saut de ligne dans la saisie (affiché *⏎*), *Entrée* ou *Ctrl+Entrée* envoie ; demande le protocole clavier kitty (Ghostty, kitty, WezTerm, foot), sinon coller un texte multiligne ; avec *multiline on*, ouvre la zone de saisie étendue (*↑*/*↓* y déplacent le curseur de ligne en ligne, un collage multiligne y propose insertion telle quelle ou en bloc de code) |
| *Tab* | complète les commandes, les noms de conversations et les thèmes |
| *PgUp*, *PgDn* | défilement de la fenêtre |
| *Ctrl+L* | repeint l'écran |
| *Ctrl+C*, */quit* | quitte |
| *molette* | fait défiler la fenêtre de 3 lignes (charge l'historique en haut) ; au-dessus du panneau, passe à la fenêtre précédente/suivante dans l'ordre du panneau |
| *clic* sur un lien | ouvre le lien avec *xdg-open* |
| *clic* sur une image affichée en ligne | aperçu plein écran, comme *v* (*Maj+clic* garde la sélection de texte du terminal) ; *o* et */open* gardent *xdg-open* |
| *Ctrl+V* | colle une image du presse-papier pour l'envoyer, ou un texte dans la saisie |

Les sorties longues (*/chats*, */theme list*, */help*, */window list*) s'affichent page par page : *Espace* ou *Entrée* pour la suite, *q* pour tout afficher, *Échap* pour abandonner ; la molette affiche tout et reprend la main.

Un collage de plus de 64 Ko est refusé ; un collage de plusieurs lignes ne part pas tout seul : la ligne de saisie propose *(e)* envoyer tel quel, *(c)* envoyer en bloc de code, *(a)* annuler.

La touche *Tab* complète selon la commande : noms de conversations après */query*, */msg* et */join*, fenêtres après */win*, clés puis valeurs après */set*, thèmes après */theme*.

- Afficher ou modifier une option à chaud

```
/set
/set timestamps off
```

# Version 1.0

La version 1.0 couvre tout ce qui précède. Les limites connues sont listées dans **CHANGELOG.md**.

# Retour arrière

> [!warning]
> A faire uniquement si le compte doit être déconnecté de cette machine.

- Supprimer la session enregistrée

```bash
rm -f ~/.config/ttyloom/session.json ~/.config/ttyloom/session-bot.json
```

- Vérifier que le prochain lancement redemande le numéro de téléphone ou réutilise le token

# Problèmes récurrents

## La fenêtre 0 se remplit d'avertissements

Les avertissements de la bibliothèque Telegram ne sont plus affichés en fenêtre 0 ; ils vont dans le journal interne.

```
*** WARN peer inconnu : get users: flood wait argument is too big (2m43s > 15s)
```

- Consulter le journal avec */debug* ; une limitation *FLOOD_WAIT* suspend les résolutions réseau le temps annoncé, signalé une seule fois par *FLOOD_WAIT 2m43s : résolutions réseau suspendues*

## Alt+A ne répond pas

Certains environnements de bureau capturent *Alt+lettre* avant le terminal. Les bascules sont sur les touches de fonction : *F6* pour la vue agrégée, *F4* pour le mode des images.

## réaction non disponible ici

Telegram n'accepte qu'une liste fixe de réactions, que chaque salon peut restreindre ; le sélecteur *r* ne propose que celles-là. Dans **Messages enregistrés**, une réaction sert de tag et demande un compte Premium.

```
*** réaction réservée à Telegram Premium ici
```

## Les images s'affichent en demi-blocs dans Ghostty

**ttyloom** interroge le terminal au démarrage ; sous **tmux** ou **screen** la réponse du protocole kitty n'arrive pas et l'affichage se replie en demi-blocs.

```
*** terminal 200x60, cellule 0x0 px, kitty graphics false, clavier kitty false → images : halfblock, thème : Bluloco Dark
```

- Lancer **ttyloom** directement dans **Ghostty** ou **kitty**, hors multiplexeur

Une cellule *0x0* avec *kitty graphics true* est un autre cas : la taille de cellule n'est pas encore connue au tout premier démarrage dans une fenêtre neuve. Le mode voulu est repris tout seul au premier redimensionnement qui la porte, et les médias sont redécodés ; *F4* fait la même chose à la main.

## Maj+Entrée n'insère pas de saut de ligne

Le saut de ligne dans la saisie demande le protocole clavier kitty. La bannière de démarrage en donne l'état, *clavier kitty true* quand le terminal a répondu à la sonde.

```
*** terminal 200x60, cellule 9x18 px, kitty graphics true, clavier kitty true → images : kitty, thème : Bluloco Dark
```

- Lancer **ttyloom** hors **tmux** et **screen**, qui ne relaient pas la sonde
- Utiliser *Alt+Entrée*, qui insère le même saut de ligne sans le protocole

## Les GIF restent des étiquettes

Les GIF Telegram sont des vidéos MP4 : sans *ffmpeg*, l'étiquette indique la cause.

```
[gif 0:03 · 1,2 Mo] · erreur : ffmpeg absent
```

- Installer le paquet *ffmpeg*

## BOT_METHOD_INVALID dans la fenêtre 0

Le mode bot a tenté une opération réservée aux comptes utilisateur, le plus souvent */history* ou */msg* vers un utilisateur qui n'a jamais écrit au bot.

- Utiliser un compte utilisateur pour ces opérations, ou attendre que l'utilisateur écrive au bot

## api_id / api_hash manquants

```
ttyloom : api_id / api_hash manquants : crée une application sur https://my.telegram.org puis renseigne ~/.config/ttyloom/config.toml (ou TG_API_ID / TG_API_HASH)
```

- Editer le fichier **~/.config/ttyloom/config.toml**

# Sources


[^1]: [core.telegram.org/api](https://core.telegram.org/api)
[^2]: [sw.kovidgoyal.net/kitty/graphics-protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/)
