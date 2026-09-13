# Contributing to TTYloom

**Testers are welcome!** Bug reports, terminal compatibility checks, translations
and focused code changes all help. Issues and pull requests may be written in
English or French. Please follow the [code of conduct](CODE_OF_CONDUCT.md).

## Test and report a problem

Try the [latest release](https://github.com/govlog/ttyloom/releases/latest).
Useful areas include Telegram and Discord login, keyboard and mouse input,
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
model and UI live under `internal/`.

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
CGO_ENABLED=0 go build -trimpath -tags nospell -o /tmp/ttyloom ./cmd/ttyloom
```

Normal builds and race tests need Hunspell development files. Without them,
use `CGO_ENABLED=0 go test -tags nospell ./...` and state that limitation in your
pull request. Describe the problem, resulting behavior and checks performed.

Keep credentials and personal working notes out of commits. Original code is
[MIT licensed](LICENSE.md); preserve separate licenses of copied code and
dependencies. If adding one, update [third-party notices](THIRD_PARTY_NOTICES.md).
For publishing, see [release instructions](docs/releases.md).
