#!/usr/bin/env bash
set -euo pipefail
umask 022

cd "$(git rev-parse --show-toplevel)"
version=${1:?Usage: scripts/release.sh vMAJOR.MINOR.PATCH}
if [[ ! $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Expected a release version such as v1.1.0" >&2
    exit 1
fi
if [[ -n $(git status --porcelain --untracked-files=normal) ]]; then
    echo "Commit changes before building a release." >&2
    exit 1
fi
if [[ -n $(git ls-files docs/audit docs/superpowers docs/notes .superpowers .worktrees) ]]; then
    echo "Private working documents must not be tracked in a release." >&2
    exit 1
fi
commit=$(git rev-parse HEAD)
if [[ $(git rev-parse "$version^{commit}") != "$commit" ]]; then
    echo "The release tag must point to HEAD." >&2
    exit 1
fi

out="$PWD/dist/$version"
mkdir -p "$out"
if [[ -n $(ls -A "$out") ]]; then
    echo "Release output already exists: $out" >&2
    exit 1
fi
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
timestamp=$(git show -s --format=%ct HEAD)

# Build the committed source only; ignored local files cannot enter a release.
git archive HEAD | tar -xf - -C "$stage"
for arch in amd64 arm64; do
    name="ttyloom_${version#v}_linux_$arch"
    package="$stage/$name"
    mkdir "$package"
    (
        cd "$stage"
        CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOAMD64=v1 GOARM64=v8.0 \
            go build -mod=readonly -trimpath -buildvcs=false -tags nospell \
            -ldflags "-s -w -X main.version=$version -X main.commit=$commit" \
            -o "$package/ttyloom" ./cmd/ttyloom
    )
    for doc in README.md README.fr.md CHANGELOG.md TODO.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md LICENSE.md THIRD_PARTY_NOTICES.md docs licenses; do
        cp -R "$stage/$doc" "$package/"
    done
    printf 'Version: %s\nCommit: %s\nToolchain: %s\nTarget: linux/%s\nCGO_ENABLED=0; tags=nospell\n' \
        "$version" "$commit" "$(go version)" "$arch" > "$package/BUILD.txt"
    tar --sort=name --mtime="@$timestamp" --owner=0 --group=0 --numeric-owner \
        -cf - -C "$stage" "$name" | gzip -n > "$out/$name.tar.gz"
done
git archive --format=tar --prefix="ttyloom_${version#v}_source/" HEAD \
    | gzip -n > "$out/ttyloom_${version#v}_source.tar.gz"
(
    cd "$out"
    sha256sum ./*.tar.gz > SHA256SUMS
)
echo "Release archives and SHA256SUMS: $out"
