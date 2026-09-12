# Connecter vos comptes

[English](authentication.md) · [Retour au README](../README.fr.md) · [Manuel complet](guide.fr.md)

TTYloom peut connecter Telegram, Discord, ou les deux. Lancez-le une première
fois pour créer `~/.config/ttyloom/config.toml`, puis modifiez ce fichier.
`TTYLOOM_DIR` permet de choisir un autre répertoire. Les options générales
TOML doivent rester avant les sections comme `[discord]`.

## Telegram

### Compte personnel : API ID et API hash

Un compte utilisateur Telegram utilise **les identifiants d’une application et
une session de connexion**. Il n’utilise pas de token BotFather.

1. Créez votre compte dans une application Telegram officielle et connectez-vous.
2. Ouvrez [my.telegram.org](https://my.telegram.org/) et identifiez-vous.
3. Ouvrez **API development tools**, remplissez le formulaire, puis copiez
   **api_id** et **api_hash**. Voir la [procédure officielle](https://core.telegram.org/api/obtaining_api_id).
4. Modifiez les clés existantes au début de `config.toml` :

   ```toml
   api_id = 123456                    # votre identifiant numérique
   api_hash = "VOTRE_API_HASH_TELEGRAM"
   bot_token = ""                    # vide pour un compte personnel
   ```

5. Lancez `./ttyloom`. Sur le téléphone, ouvrez **Paramètres → Appareils →
   Connecter un appareil** et scannez le QR code. Vous pouvez aussi appuyer sur
   Entrée dans TTYloom pour saisir le numéro avec son indicatif international,
   puis le code envoyé par Telegram. Saisissez le mot de passe 2FA si demandé.

Les noms des menus varient selon la langue et la version de l’application.
La session est enregistrée dans `session.json`, dans le répertoire de
configuration. Ce fichier donne accès au compte : gardez-le privé et hors de
Git. Il n’est pas nécessaire d’extraire un token du navigateur ou du téléphone.

`TG_API_ID` et `TG_API_HASH` peuvent remplacer les valeurs du fichier pour un
lancement. Ces valeurs ne sont pas recopiées dans la configuration. Évitez de
mettre des secrets dans l’historique du shell, un script partagé ou un ticket.

### Bot Telegram : obtenir un token BotFather

1. Ouvrez le compte officiel [@BotFather](https://t.me/BotFather) dans Telegram.
2. Envoyez `/newbot`, puis choisissez le nom affiché et le nom d’utilisateur.
3. Conservez le token retourné dans un emplacement privé. Voir le
   [guide officiel Telegram](https://core.telegram.org/bots/tutorial#obtain-your-bot-token).
4. Gardez aussi `api_id` et `api_hash` : TTYloom utilise MTProto et a besoin
   des identifiants de l’application même en mode bot. Configurez :

   ```toml
   bot_token = "VOTRE_TOKEN_BOTFATHER"
   ```

   Vous pouvez utiliser `TG_BOT_TOKEN` au lieu d’enregistrer le token dans le fichier.

5. Lancez TTYloom et envoyez un message au bot depuis un autre compte.

Le bot utilise `session-bot.json`, distinct de la session utilisateur. Il ne
peut pas lister les conversations, relire l’historique du compte ou faire une
recherche globale. Il ne peut pas engager une conversation privée avec un
utilisateur quelconque qui ne l’a pas contacté.

### Accès perdu ou identifiants exposés

Pour un compte personnel, révoquez la session concernée dans les paramètres
**Appareils** de Telegram. Arrêtez TTYloom, mettez son fichier de session de
côté, puis reconnectez-vous. Pour un bot, révoquez et remplacez le token via
BotFather. Ne joignez aucun fichier de session, code, API hash ou token à un
rapport de problème.

## Discord

### Quel token est accepté ?

L’adaptateur `protocols/dsc` utilise un **token de compte utilisateur normal**
avec ningen. Il ne gère **ni compte bot ni connexion OAuth2**. Un token de bot
obtenu dans le Developer Portal, un secret d’application ou un jeton OAuth2 ne
remplace donc pas le token utilisateur attendu.

Discord indique que l’utilisation d’un token utilisateur dans une autre
application peut entraîner une suspension ou une suppression du compte.
L’intégration est non officielle. Consultez les [conseils de sécurité Discord](https://discord.com/safety/360044104071-Tips-against-spam-and-hacking)
et la [politique sur les self-bots](https://support.discord.com/hc/en-us/articles/115002192352-Automated-User-Accounts-Self-Bots).

### Se connecter par QR code (recommandé)

Écrivez une section `[discord]` vide à la fin de `config.toml` et lancez
TTYloom. Un QR code s’affiche en fenêtre 0 ; scannez-le depuis l’appli Discord
(**Paramètres → Scanner un QR code**) et confirmez sur le téléphone. C’est
l’authentification à distance du client officiel : TTYloom devient un appareil
à part entière, listé dans **Paramètres → Appareils**, et le token reçu est
enregistré dans `~/.config/ttyloom/discord.token` en mode `0600`. Rien n’est lu
dans un navigateur ni dans le client de bureau. `/discord logout` ferme cette
session côté serveur et supprime le fichier ; `/discord login` réaffiche le QR.

### Obtenir manuellement votre propre token

Cette procédure concerne uniquement **votre propre session de navigateur déjà
connectée**. TTYloom ne lit pas les profils du navigateur, ne récupère pas les
identifiants et ne demande pas votre mot de passe Discord.

1. Ouvrez [Discord dans le navigateur](https://discord.com/app) et connectez-vous normalement.
2. Ouvrez les outils de développement (**F12** ou **Ctrl+Maj+I**), puis l’onglet
   **Réseau / Network**. Choisissez **Fetch/XHR** si ce filtre est disponible.
3. Rechargez la page ou ouvrez un salon pour provoquer une requête API normale.
4. Sélectionnez une requête vers `https://discord.com/api/…`, par exemple pour
   charger vos salons ou messages. Ouvrez **En-têtes / Headers → En-têtes de
   requête / Request Headers**.
5. Repérez **Authorization**. La valeur d’une requête utilisateur authentifiée
   est le token attendu. Copiez uniquement cette valeur dans votre gestionnaire
   de mots de passe. N’utilisez pas « Copier en cURL », n’exportez pas de HAR et
   ne collez pas les en-têtes dans un ticket : cela peut exposer vos accès.
6. Fermez les outils et videz le presse-papiers après l’enregistrement.

Il s’agit d’une inspection manuelle des requêtes du client web, pas d’une
procédure officielle de délivrance de token par Discord. Les libellés et
requêtes peuvent changer. Si l’en-tête est absent, n’installez pas d’extension
qui promet d’extraire un token et ne collez pas de script dans la console.

### Stocker le token et se connecter

Avec un coffre [pass](https://www.passwordstore.org/) déjà configuré :

```bash
pass insert discord/token
```

Collez le token à l’invite masquée. L’entrée doit contenir uniquement le token,
sans guillemets, préfixe `Bot`/`Bearer` ou ligne descriptive. Ajoutez cette
section **à la fin** de `config.toml` :

```toml
[discord]
token_cmd = "pass show discord/token"
```

Lancez `./ttyloom`. Pour Discord seul, gardez `api_id = 0`, `api_hash = ""` et
`bot_token = ""`, et retirez les éventuelles variables d’identification `TG_*`.

Un autre gestionnaire ou un exécutable local peut fournir le token. La commande
s’exécute **sans shell**, découpe les arguments sur les espaces et s’arrête après
30 secondes. Les tubes, redirections, variables et guillemets de shell ne sont
pas interprétés. Utilisez un script exécutable si nécessaire. TTYloom retire
les blancs autour du résultat et ne stocke pas le token dans `config.toml` ;
son code ne journalise pas volontairement sa valeur.

En cas d’échec, vérifiez en privé l’entrée du gestionnaire et la commande.
Reconnectez-vous au client officiel et remplacez le token sauvegardé si
nécessaire ; `/discord login` relance ensuite la commande sans quitter TTYloom.
Si le token a été exposé, sécurisez le compte dans les paramètres Discord et
remplacez cet accès.

### Si vous cherchez un token de bot Discord

Pour un projet de bot distinct, créez une application dans le
[Discord Developer Portal](https://discord.com/developers/applications), ouvrez
**Bot**, puis utilisez **Reset Token**. Conservez le résultat en privé. Voir le
[guide officiel des bots](https://docs.discord.com/developers/quick-start/getting-started).
**L’adaptateur Discord actuel de TTYloom ne peut pas utiliser ce token.**
Il faudrait une implémentation fondée sur les API bot officielles.

## IRC

IRC ne demande aucun token : un pseudo, et un mot de passe quand ce pseudo est
enregistré auprès des services du réseau (NickServ). `/irc add` demande les
deux dans un formulaire et écrit une table `[[irc]]` à la fin de
`config.toml` (mode `0600`) ; le mot de passe reste dans ce fichier et ne va
nulle part ailleurs. TTYloom se connecte directement au serveur en TLS (port
6697 sur la plupart des réseaux), sans bouncer.

Avec un mot de passe, TTYloom s'identifie par **SASL PLAIN** quand le serveur
l'offre, ce qui est le cas de Libera.Chat, OFTC et de tout ircd moderne ; un
serveur sans SASL reçoit un `NickServ IDENTIFY` juste après l'enregistrement.
Un pseudo déjà pris est suffixé par le serveur. Laissez le mot de passe vide
sur un réseau où le pseudo n'est pas enregistré.

Pour ne plus utiliser un réseau, `/irc disconnect <nom>` garde sa table ;
retirer la table de `config.toml` l'oublie, mot de passe et liste de salons
compris. Changez le mot de passe auprès des services du réseau (`/msg
NickServ SET PASSWORD` sur la plupart d'entre eux) s'il a été exposé.
