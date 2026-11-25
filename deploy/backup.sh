#!/bin/sh
# A coherent backup of the two things that cannot be rebuilt from the source:
# the SQLite database and the original documents it references. The database is
# copied with SQLite's online backup API, so a writer does not tear it, and the
# object list is read from that copy, so the bytes copied are exactly the ones
# the copied database references. Objects are content-addressed and immutable:
# an object added after the snapshot is an extra file, never a wrong one.
set -eu
umask 077

db=${CONTRATOS_DB:-/var/lib/la-mordida/contratos.db}
objects=${CONTRATOS_OBJECTS:-/var/lib/la-mordida/objects}
backup=${CONTRATOS_BACKUP:-/var/backups/la-mordida}
stamp=$(date -u +%Y%m%dT%H%M%SZ)

if [ ! -f "$db" ]; then
    echo "backup: database $db does not exist" >&2
    exit 1
fi
if [ ! -d "$objects" ]; then
    echo "backup: object directory $objects does not exist" >&2
    exit 1
fi

mkdir -p "$backup"
tmp=$(mktemp -d "$backup/.tmp.XXXXXX")
cleanup() {
    rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

python3 - "$db" "$objects" "$tmp" "$stamp" <<'PY'
import hashlib
import os
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path

db_path, objects_root, out, stamp = sys.argv[1:5]
out = Path(out)
objects_root = Path(objects_root)
copy = out / "contratos.db"

source = sqlite3.connect(db_path)
source.execute("PRAGMA query_only = ON")
target = sqlite3.connect(copy)
source.backup(target)
target.close()
source.close()

check = sqlite3.connect(copy)
result = check.execute("PRAGMA integrity_check").fetchone()[0]
if result != "ok":
    raise SystemExit("backup: copied database failed integrity_check: %s" % result)
objects = check.execute(
    "SELECT sha256, bytes FROM objeto ORDER BY sha256"
).fetchall()
check.close()

hash = hashlib.sha256()
with copy.open("rb") as handle:
    for chunk in iter(lambda: handle.read(1024 * 1024), b""):
        hash.update(chunk)
db_sha256 = hash.hexdigest()

missing = []
object_bytes = 0
for sha, size in objects:
    source_file = objects_root / sha[:2] / sha
    if not source_file.is_file():
        missing.append(sha)
        continue
    destination = out / "objects" / sha[:2] / sha
    destination.parent.mkdir(parents=True, exist_ok=True)
    object_hash = hashlib.sha256()
    written = 0
    with source_file.open("rb") as original, destination.open("wb") as stored:
        for chunk in iter(lambda: original.read(1024 * 1024), b""):
            object_hash.update(chunk)
            stored.write(chunk)
            written += len(chunk)
    if object_hash.hexdigest() != sha:
        raise SystemExit("backup: object %s does not hash to its name" % source_file)
    if written != size:
        raise SystemExit(
            "backup: object %s is %d bytes, database says %d" % (sha, written, size)
        )
    object_bytes += written

manifest = out / "manifest.txt"
with manifest.open("w", encoding="utf-8") as handle:
    handle.write("stamp=%s\n" % stamp)
    handle.write("created_utc=%s\n" % datetime.now(timezone.utc).isoformat())
    handle.write("source_db=%s\n" % db_path)
    handle.write("db_sha256=%s\n" % db_sha256)
    handle.write("db_bytes=%d\n" % copy.stat().st_size)
    handle.write("objects=%d\n" % (len(objects) - len(missing)))
    handle.write("object_bytes=%d\n" % object_bytes)
    handle.write("missing=%d\n" % len(missing))
    handle.write("integrity=ok\n")
    for sha in missing:
        handle.write("missing_object=%s\n" % sha)

print(
    "backup: %d objects, %d bytes, database %d bytes, integrity ok"
    % (len(objects) - len(missing), object_bytes, copy.stat().st_size)
)
if missing:
    raise SystemExit(
        "backup: %d referenced objects are missing; first is %s"
        % (len(missing), missing[0])
    )
PY

destination="$backup/$stamp"
if [ -e "$destination" ]; then
    destination="$destination-$$"
fi
mv "$tmp" "$destination"
rm -f "$backup/.latest"
ln -s "$(basename "$destination")" "$backup/.latest"
mv -Tf "$backup/.latest" "$backup/latest"
echo "backup: $destination"
