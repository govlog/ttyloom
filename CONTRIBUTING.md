# Contributing to TTYloom

**Testers are welcome!** Bug reports, terminal compatibility checks, translations
and focused code changes all help. Issues and pull requests may be written in
English or French. Please follow the [code of conduct](CODE_OF_CONDUCT.md).

## Test and report a problem

Try the [newest release](https://github.com/govlog/ttyloom/releases).
Useful areas include Telegram, Discord and IRC login, keyboard and mouse input,
inline images, search, reconnects, and both interface languages.

Search [existing issues](https://github.com/govlog/ttyloom/issues) first. Include
`ttyloom --version`, your OS and CPU architecture, terminal name and version,
the network involved, steps to reproduce, and expected versus actual behavior.
A small screenshot or log excerpt can help; remove private content first.
Never attach tokens, session files, a complete configuration or private
conversations. Report vulnerabilities privately through [SECURITY.md](SECURITY.md).

## Make a change

Build using the [README instructions](README.md#build-from-source). The project
uses one Go module; network adapters live under `protocols/`, and the shared
model and UI live under `internal/`. [Writing a network module](docs/modules.md)
explains how a network plugs into the client.

Keep each pull request focused on one problem. Reuse existing helpers and the
standard library. Add a regression test when it demonstrates the behavior being
fixed. Update both interface languages and the documentation when affected. Discuss
a large feature or new protocol in an issue before investing substantial work.

Run checks relevant to your changes. The complete CI checks are:

```bash
go test -race ./...
go test -tags nospell ./...
go vet ./...
go mod tidy -diff
python3 scripts/licenses.py --check
CGO_ENABLED=0 go build -trimpath -tags nospell -o /tmp/ttyloom ./cmd/ttyloom
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

Normal builds and race tests need Hunspell development files. Without them,
use `CGO_ENABLED=0 go test -tags nospell ./...` and state that limitation in your
pull request. Tests that need FFmpeg or the French and US English dictionaries
are skipped without them; CI installs `ffmpeg`, `hunspell-fr` and
`hunspell-en-us`. Describe the problem, resulting behavior and checks performed.

Keep credentials and personal working notes out of commits. Original code is
[MIT licensed](LICENSE.md); preserve separate licenses of copied code and
dependencies. If adding one, update [third-party notices](THIRD_PARTY_NOTICES.md);
after a change of `go.mod`, `python3 scripts/licenses.py` refreshes `licenses/go`.
For publishing, see [release instructions](docs/releases.md).
