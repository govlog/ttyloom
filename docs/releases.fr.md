# Publier une version

[English](releases.md) · [Contribuer](../CONTRIBUTING.md#français)

TTYloom publie des binaires Linux portables pour x86-64 (`amd64`) et ARM64
(`arm64`). Ils utilisent `CGO_ENABLED=0` et `nospell` ; Hunspell est disponible
en compilant depuis les sources. FFmpeg, les outils de presse-papier et les
dictionnaires sont installés séparément.

Chaque version contient deux archives binaires, une archive des sources du même
commit et `SHA256SUMS`. Les archives binaires incluent les READMEs, la documentation,
les notices et les licences. `BUILD.txt` indique version, commit, compilateur Go
et options ; `ttyloom --version` affiche la version et le commit.

## Préparer et publier

1. Mettre à jour le journal des changements et la version dans les deux READMEs
   et manuels. Vérifier les notices si `go.mod`, du code copié, des illustrations
   ou les plateformes de compilation changent.
2. Exécuter les vérifications de `.github/workflows/ci.yml`, puis committer.
3. Créer un tag et le pousser, en remplaçant `v1.1.0` par la nouvelle version :

   ```bash
   git tag -a v1.1.0 -m 'TTYloom v1.1.0'
   git push origin main
   git push origin v1.1.0
   ```

4. Attendre CI et le workflow Release. Celui-ci teste le build portable, produit
   les archives, vérifie les sommes de contrôle, lance le binaire x86-64 et le
   binaire ARM64 sous QEMU, puis crée un **brouillon** de version GitHub.
5. Vérifier les fichiers et les notes, puis publier depuis GitHub ou avec :

   ```bash
   gh release edit v1.1.0 --draft=false --latest
   ```

Ces tests de lancement ne remplacent pas les essais avec des comptes réels ou
sur du matériel ARM64 natif. Ne pas remplacer des fichiers publiés ni déplacer
un tag publié : créer une nouvelle version pour les corrections.

## Compiler localement

Sur Linux, avec la version Go de `go.mod`, Git, Bash, GNU tar et gzip :

```bash
bash scripts/release.sh v1.1.0
cd dist/v1.1.0
sha256sum --check SHA256SUMS
```

Le tag doit pointer vers le commit courant, le répertoire de travail doit être
propre et le dossier de sortie vide. Le script compile une archive des sources
committées : les fichiers locaux ne peuvent pas entrer dans les paquets. Il
refuse les notes de travail internes suivies par Git. La sortie est dans
`dist/`, ignoré par Git.

La compilation utilise `-trimpath` et indique explicitement le commit. Les dates,
l’ordre, les permissions et les propriétaires des archives sont fixés. Utiliser
la même version corrective de Go pour comparer les sommes entre machines.
`SHA256SUMS` détecte les téléchargements corrompus ou modifiés ; ce n’est pas une
signature numérique.
