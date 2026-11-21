#!/bin/sh
set -eu
export LC_ALL=C

if [ "$#" -ne 1 ]; then
    echo 'Usage: sh scripts/package.sh MAJOR.MINOR.PATCH' >&2
    exit 2
fi

version=$1
if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-step[0-9]{2}-[0-9]{8}-[0-9]{4}-r[0-9]+)?$'; then
    echo 'Version must be MAJOR.MINOR.PATCH with an optional step and timestamp suffix.' >&2
    exit 2
fi

for tool in go tar gzip sha256sum mktemp sort; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'Required tool is missing: %s\n' "$tool" >&2
        exit 3
    fi
done

repo=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
output_dir=$repo/dist
ui_dist=$repo/frontend/dist
package=debian-router-b5-2840-$version
if [ -e "$output_dir/$version" ]; then
    printf 'Package already exists: %s\n' "$output_dir/$version" >&2
    exit 4
fi

for asset in index.html assets/app.js assets/app.css; do
    if [ ! -f "$ui_dist/$asset" ] || [ -L "$ui_dist/$asset" ]; then
        printf 'Required UI build output is missing or invalid: %s\n' "$ui_dist/$asset" >&2
        echo 'Build the frontend before packaging with: cd frontend && npm ci && npm run build' >&2
        exit 5
    fi
done

mkdir -p "$output_dir"
stage=$(mktemp -d "$output_dir/.package.XXXXXXXX")
trap 'rm -r -- "$stage"' EXIT HUP INT TERM
mkdir "$stage/$package"
printf '%s\n' "$version" > "$stage/$package/VERSION"
printf '%s\n' 'pppd' > "$stage/$package/PPPOE_SUPPORT"
printf '%s\n' 'dnsmasq-base nftables' > "$stage/$package/LAN_SUPPORT"
printf '%s\n' '1.14.4' '699a1b510aa4396b2176d32d1181920399ebf1e267835da6f3b598d0f7c24968' \
    > "$stage/$package/SING_BOX_SUPPORT"
printf '%s\n' 'profile-v1' > "$stage/$package/PROXY_ROUTING_SUPPORT"
mkdir -p "$stage/$package/web/assets"
install -m 0644 "$ui_dist/index.html" "$stage/$package/web/index.html"
install -m 0644 "$ui_dist/assets/app.js" "$stage/$package/web/assets/app.js"
install -m 0644 "$ui_dist/assets/app.css" "$stage/$package/web/assets/app.css"

(cd "$repo/backend" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -buildvcs=false -ldflags=-buildid= \
    -o "$stage/$package/router-api" ./cmd/router-api)
(cd "$repo/backend" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -buildvcs=false -ldflags=-buildid= \
    -o "$stage/$package/router-agent" ./cmd/router-agent)

tar --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner \
    -C "$stage" -cf "$stage/$package.tar" "$package"
gzip -n "$stage/$package.tar"
expected_listing=$(printf '%s\n' "$package/" "$package/VERSION" "$package/PPPOE_SUPPORT" "$package/LAN_SUPPORT" \
    "$package/SING_BOX_SUPPORT" "$package/PROXY_ROUTING_SUPPORT" "$package/router-api" \
    "$package/router-agent" \
    "$package/web/" "$package/web/assets/" "$package/web/assets/app.css" \
    "$package/web/assets/app.js" "$package/web/index.html" | sort)
if [ "$(tar -tzf "$stage/$package.tar.gz" | sort)" != "$expected_listing" ]; then
    echo 'Release package contains unexpected files.' >&2
    exit 6
fi
(cd "$stage" && sha256sum "$package.tar.gz" > "$package.tar.gz.sha256")
rm -r -- "$stage/$package"
mv -T -- "$stage" "$output_dir/$version"
trap - EXIT HUP INT TERM
printf 'Package: %s\n' "$output_dir/$version/$package.tar.gz"
printf 'SHA-256: %s\n' "$output_dir/$version/$package.tar.gz.sha256"
