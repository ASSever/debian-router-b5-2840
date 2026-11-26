#!/bin/sh
# Based on Flowseal's general (ALT11) profile, pinned in install.sh.
# SPDX-License-Identifier: MIT
# Copyright (c) 2016-2026 bol-van
# Copyright (c) 2024-2026 Flowseal
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.
set -eu

case "$#:${2-}" in
    1:|2:--dry-run) ;;
    *) exit 2 ;;
esac

assets=$1
shift
bin=$assets/bin
lists=$assets/lists
google_tls=$bin/tls_clienthello_www_google_com.bin
sferum_tls=$bin/tls_clienthello_www_sferum_ru.bin
google_quic=$bin/quic_initial_www_google_com.bin
discord_udp=$bin/ACTIVE_DISCORD_UDP.bin
excluded_hosts=$lists/list-exclude.txt
excluded_ips=$lists/ipset-exclude.txt

config=${ZAPRET_CONFIG_DIR:-/etc/debian-router-b5-2840/zapret/current}
profile=alt11
custom_hosts=
if [ -e "$config" ] || [ -L "$config" ]; then
    config=$(readlink -e -- "$config")
    profile=$(cat "$config/profile")
    [ -r "$config/exclude-hosts.txt" ] || exit 2
    if [ -s "$config/exclude-hosts.txt" ]; then
        custom_hosts=--hostlist-exclude=$config/exclude-hosts.txt
    fi
fi

skip_udp=
case "$profile" in
    alt11) ;;
    alt11-tcp) skip_udp=--skip ;;
    *) echo 'Unknown Zapret profile.' >&2; exit 2 ;;
esac

# Keep the original ALT11 strategy order. The TCP profile disables its UDP strategies.
# Raw Discord/STUN has no hostname, so its separate strategy takes no domain list.
exec "$bin/nfqws" "$@" \
    --qnum=62232 --dpi-desync-fwmark=0x40000000 \
    ${skip_udp:+"$skip_udp"} ${custom_hosts:+"$custom_hosts"} \
    --filter-udp=443 --hostlist="$lists/list-general.txt" \
    --hostlist-exclude="$excluded_hosts" --ipset-exclude="$excluded_ips" \
    --dpi-desync=fake --dpi-desync-repeats=11 --dpi-desync-fake-quic="$google_quic" --new \
    ${skip_udp:+"$skip_udp"} \
    --filter-udp=19294-19344,50000-50100 --filter-l7=discord,stun \
    --dpi-desync=fake --dpi-desync-repeats=6 \
    --dpi-desync-fake-discord="$discord_udp" --dpi-desync-fake-stun="$discord_udp" --new \
    ${custom_hosts:+"$custom_hosts"} \
    --filter-tcp=2053,2083,2087,2096,8443 --hostlist-domains=discord.media \
    --dpi-desync=fake,multisplit --dpi-desync-split-seqovl=681 --dpi-desync-split-pos=1 \
    --dpi-desync-fooling=ts --dpi-desync-repeats=8 \
    --dpi-desync-split-seqovl-pattern="$google_tls" --dpi-desync-fake-tls="$google_tls" --new \
    ${custom_hosts:+"$custom_hosts"} \
    --filter-tcp=443 --hostlist="$lists/list-google.txt" --ip-id=zero \
    --dpi-desync=fake,multisplit --dpi-desync-split-seqovl=681 --dpi-desync-split-pos=1 \
    --dpi-desync-fooling=ts --dpi-desync-repeats=8 \
    --dpi-desync-split-seqovl-pattern="$google_tls" --dpi-desync-fake-tls="$google_tls" --new \
    ${custom_hosts:+"$custom_hosts"} \
    --filter-tcp=80,443 --hostlist="$lists/list-general.txt" \
    --hostlist-exclude="$excluded_hosts" --ipset-exclude="$excluded_ips" \
    --dpi-desync=fake,multisplit --dpi-desync-split-seqovl=664 --dpi-desync-split-pos=1 \
    --dpi-desync-fooling=ts --dpi-desync-repeats=8 --dpi-desync-split-seqovl-pattern="$sferum_tls" \
    --dpi-desync-fake-tls="$bin/stun2.bin" --dpi-desync-fake-tls="$sferum_tls" \
    --dpi-desync-fake-http="$sferum_tls" --new \
    ${skip_udp:+"$skip_udp"} ${custom_hosts:+"$custom_hosts"} \
    --filter-udp=443 --ipset="$lists/ipset-all.txt" \
    --hostlist-exclude="$excluded_hosts" --ipset-exclude="$excluded_ips" \
    --dpi-desync=fake --dpi-desync-repeats=11 --dpi-desync-fake-quic="$google_quic" --new \
    ${custom_hosts:+"$custom_hosts"} \
    --filter-tcp=80,443,8443 --ipset="$lists/ipset-all.txt" \
    --hostlist-exclude="$excluded_hosts" --ipset-exclude="$excluded_ips" \
    --dpi-desync=fake,multisplit --dpi-desync-split-seqovl=664 --dpi-desync-split-pos=1 \
    --dpi-desync-fooling=ts --dpi-desync-repeats=8 --dpi-desync-split-seqovl-pattern="$sferum_tls" \
    --dpi-desync-fake-tls="$bin/stun2.bin" --dpi-desync-fake-tls="$sferum_tls" \
    --dpi-desync-fake-http="$sferum_tls"
