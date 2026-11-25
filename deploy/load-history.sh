#!/bin/sh
# Import a channel month by month from January 2020. Each month is one PLACSP
# ZIP archive, downloaded once and hashed; the same window is imported with the
# normal command, so a failed or interrupted month is retried by running the
# script again. The archive is kept by default: it is the original the rows
# came from and re-downloading it is the slow part.
set -eu

base=https://contrataciondelestado.es/sindicacion
work=${CONTRATOS_WORK:-/var/lib/la-mordida/archives}
db=${CONTRATOS_DB:-/var/lib/la-mordida/contratos.db}
objects=${CONTRATOS_OBJECTS:-/var/lib/la-mordida/objects}
binary=${CONTRATOS_BIN:-/opt/la-mordida/contratos}
lock=${CONTRATOS_LOCK:-/var/lib/la-mordida/write.lock}
channel=
from=
until=
archive_docs=0

usage() {
    echo "usage: load-history.sh -channel CHANNEL -from YYYY-MM -until YYYY-MM [-dir DIR] [-db DB] [-objects DIR] [-archive-docs]" >&2
    exit 2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
    -channel) channel=$2; shift 2 ;;
    -from) from=$2; shift 2 ;;
    -until) until=$2; shift 2 ;;
    -dir) work=$2; shift 2 ;;
    -db) db=$2; shift 2 ;;
    -objects) objects=$2; shift 2 ;;
    -archive-docs) archive_docs=1; shift ;;
    *) usage ;;
    esac
done

case "$channel" in
sindicacion_643) family=licitacionesPerfilesContratanteCompleto3 ;;
sindicacion_1044) family=PlataformasAgregadasSinMenores ;;
sindicacion_1143) family=contratosMenoresPerfilesContratantes ;;
*) usage ;;
esac
case "$from" in [0-9][0-9][0-9][0-9]-[0-9][0-9]) ;; *) usage ;; esac
case "$until" in [0-9][0-9][0-9][0-9]-[0-9][0-9]) ;; *) usage ;; esac

mkdir -p "$work"
manifest="$work/manifest-$channel.tsv"
[ -f "$manifest" ] || printf 'month\turl\tsha256\tbytes\tdownloaded_utc\n' >"$manifest"

month="$from-01"
end="$until-01"
while [ "$(date -u -d "$month" +%Y-%m)" \< "$(date -u -d "$end" +%Y-%m)" ]; do
    start_date=$(date -u -d "$month" +%Y-%m-%d)
    next_date=$(date -u -d "$month +1 month" +%Y-%m-%d)
    ym=$(date -u -d "$month" +%Y%m)
    url="$base/$channel/${family}_${ym}.zip"
    zip="$work/${family}_${ym}.zip"

    if [ ! -s "$zip" ] || ! python3 -c 'import sys, zipfile; zipfile.ZipFile(sys.argv[1]).testzip()' "$zip" 2>/dev/null; then
        echo "load: downloading $url" >&2
        part="$zip.part"
        rm -f "$part"
        curl -fsSL --retry 3 --retry-delay 10 --max-time 3600 \
            -A 'Mozilla/5.0 (X11; Linux aarch64) la-mordida/1' \
            -H 'Accept-Language: es-ES,es;q=0.9' \
            -H 'Referer: https://contrataciondelestado.es/' \
            -o "$part" "$url"
        python3 -c 'import sys, zipfile; assert zipfile.ZipFile(sys.argv[1]).testzip() is None' "$part"
        mv "$part" "$zip"
        sha=$(sha256sum "$zip" | cut -d' ' -f1)
        bytes=$(wc -c <"$zip" | tr -d ' ')
        if ! grep -q "^$ym	" "$manifest"; then
            printf '%s\t%s\t%s\t%s\t%s\n' "$ym" "$url" "$sha" "$bytes" \
                "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$manifest"
        fi
    fi

    echo "load: $channel $start_date..$next_date from $zip" >&2
    flock -w 7200 "$lock" "$binary" import -db "$db" -archive "$zip" \
        -channel "$channel" -from "$start_date" -until "$next_date"
    if [ "$archive_docs" -eq 1 ]; then
        flock -w 7200 "$lock" "$binary" archive -db "$db" -objects "$objects" || true
    fi

    month=$next_date
done
