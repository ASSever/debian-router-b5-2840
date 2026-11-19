#!/bin/sh
set -eu
export LC_ALL=C

if [ "$#" -ne 1 ]; then
    echo 'Usage: sh scripts/package.sh MAJOR.MINOR.PATCH' >&2
    exit 2
fi

version=$1
if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo 'Version must have the form MAJOR.MINOR.PATCH.' >&2
    exit 2
fi

for tool in go tar gzip sha256sum mktemp; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'Required tool is missing: %s\n' "$tool" >&2
        exit 3
    fi
done

repo=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
output_dir=$repo/dist
package=debian-router-b5-2840-$version
if [ -e "$output_dir/$version" ]; then
    printf 'Package already exists: %s\n' "$output_dir/$version" >&2
    exit 4
fi

mkdir -p "$output_dir"
stage=$(mktemp -d "$output_dir/.package.XXXXXXXX")
trap 'rm -r -- "$stage"' EXIT HUP INT TERM
mkdir "$stage/$package"
printf '%s\n' "$version" > "$stage/$package/VERSION"

(cd "$repo/backend" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -buildvcs=false -ldflags=-buildid= \
    -o "$stage/$package/router-api" ./cmd/router-api)

tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$stage" -cf "$stage/$package.tar" "$package"
gzip -n "$stage/$package.tar"
(cd "$stage" && sha256sum "$package.tar.gz" > "$package.tar.gz.sha256")
rm -r -- "$stage/$package"
mv -- "$stage" "$output_dir/$version"
trap - EXIT HUP INT TERM
printf 'Package: %s\n' "$output_dir/$version/$package.tar.gz"
printf 'SHA-256: %s\n' "$output_dir/$version/$package.tar.gz.sha256"
