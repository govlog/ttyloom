# Contributing to TTYloom

[English](#english) · [Français](#français)

## English

**Testers are welcome!** Bug reports, terminal compatibility checks, translations
and focused code changes all help. Issues and pull requests may be written in
English or French. Please follow the [code of conduct](CODE_OF_CONDUCT.md).

### Test and report a problem

Try the [latest release](https://github.com/govlog/ttyloom/releases/latest).
Useful areas include Telegram and Discord login, keyboard and mouse input,
inline images, search, reconnects, and both interface languages.

Search [existing issues](https://github.com/govlog/ttyloom/issues) first. Include
`ttyloom --version`, your OS and CPU architecture, terminal name and version,
the network involved, steps to reproduce, and expected versus actual behavior.
A small screenshot or log excerpt can help; remove private content first.
Never attach tokens, session files, a complete configuration or private
conversations. Report vulnerabilities privately through [SECURITY.md](SECURITY.md).

### Make a change

Build using the [README instructions](README.md#build-from-source). The project
uses one Go module; network adapters live under `protocols/`, and the shared
model and UI live under `internal/`.

Keep each pull request focused on one problem. Reuse existing helpers and the
standard library. Add a regression test when it demonstrates the behavior being
fixed. Update both interface languages and both READMEs when affected. Discuss
a large feature or new protocol in an issue before investing substantial work.

Run checks relevant to your changes. The complete CI checks are:

```bash
go test -race ./...
go test -tags nospell ./...
go vet ./...
go mod tidy -diff
CGO_ENABLED=0 go build -trimpath -tags nospell -o /tmp/ttyloom ./cmd/ttyloom
```

Normal builds and race tests need Hunspell development files. Without them,
use `CGO_ENABLED=0 go test -tags nospell ./...` and state that limitation in your
pull request. Describe the problem, resulting behavior and checks performed.

Keep credentials and personal working notes out of commits. Original code is
[MIT licensed](LICENSE.md); preserve separate licenses of copied code and
dependencies. If adding one, update [third-party notices](THIRD_PARTY_NOTICES.md).
For publishing, see [release instructions](docs/releases.md).

## Français

**Les testeurs et testeuses sont les bienvenus !** Les rapports de bugs, essais
sur différents terminaux, traductions et corrections ciblées sont utiles.
Les issues et pull requests peuvent être rédigées en français ou en anglais.
Respectez le [code de conduite](CODE_OF_CONDUCT.md#français).

### Tester et signaler un problème

Essayez la [dernière version](https://github.com/govlog/ttyloom/releases/latest).
Vous pouvez tester la connexion Telegram et Discord, le clavier et la souris,
les images intégrées, la recherche, les reconnexions et les deux langues.

Consultez les [issues existantes](https://github.com/govlog/ttyloom/issues).
Indiquez `ttyloom --version`, le système, l’architecture du processeur, le nom et
la version du terminal, le réseau, les étapes de reproduction, le résultat
attendu et le résultat obtenu. Une petite capture ou un extrait de journal peut
aider, sans contenu privé. Ne joignez jamais de token, fichier de session,
configuration complète ou conversation privée. Pour une vulnérabilité, utilisez
le canal privé indiqué dans [SECURITY.md](SECURITY.md#français).

### Proposer une modification

Suivez les [instructions de compilation](README.fr.md#compiler-depuis-les-sources).
Le projet utilise un seul module Go : les adaptateurs réseau sont dans
`protocols/`, le modèle commun et l’interface dans `internal/`.

Traitez un problème par pull request. Réutilisez les fonctions existantes et
la bibliothèque standard. Ajoutez un test de régression s’il montre le
comportement corrigé. Mettez à jour les deux langues et les deux READMEs quand
ils sont concernés. Ouvrez une issue avant un travail important sur une
fonctionnalité ou un nouveau protocole.

Exécutez les vérifications utiles ; les commandes CI sont listées ci-dessus.
Sans les fichiers de développement Hunspell, utilisez
`CGO_ENABLED=0 go test -tags nospell ./...` et indiquez cette limite. Décrivez
le problème, le résultat et les vérifications effectuées.

Gardez les identifiants et notes personnelles hors des commits. Le code original
est sous [licence MIT](LICENSE.md). Conservez les licences du code copié et des
dépendances ; mettez à jour les [notices](THIRD_PARTY_NOTICES.md) si vous en ajoutez.
La publication est décrite dans les [instructions de version](docs/releases.fr.md).
