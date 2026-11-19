#!/bin/sh
# Run from the checkout root with a POSIX shell.
set -eu
sh -n install.sh
sh install.sh --help | grep -F 'Usage: sh install.sh' >/dev/null
expect_exit() {
    expected=$1
    shift
    actual=0
    "$@" >/dev/null 2>&1 || actual=$?
    if [ "$actual" -ne "$expected" ]; then
        printf 'Expected exit %s, got %s: %s\n' "$expected" "$actual" "$*" >&2
        exit 1
    fi
}
expect_exit 2 sh install.sh --invalid
expect_exit 2 sh install.sh --check extra

if [ "$(uname -s)" = Linux ] && [ -r /etc/os-release ]; then
    . /etc/os-release
fi
arch=$(dpkg --print-architecture 2>/dev/null || echo unknown)
if [ "${ID-}/${VERSION_ID-}/$arch" = debian/13/amd64 ]; then
    for mode in default explicit; do
        case "$mode" in
            default) report=$(sh install.sh) ;;
            explicit) report=$(sh install.sh --check) ;;
        esac
        for marker in '[Platform]' '[Storage]' '[Interfaces]' '[SSH management path]' \
            '[Services]' '[Package candidates (local APT cache)]' '[Time]' 'Stage 01 inventory finished.'; do
            printf '%s\n' "$report" | grep -F -- "$marker" >/dev/null
        done
    done
else
    expect_exit 3 sh install.sh
fi
echo 'Bootstrap checks passed.'
