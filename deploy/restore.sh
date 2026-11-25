#!/bin/sh
# Restore one backup into an empty directory and prove it byte for byte before
# pointing the service at it. The target must not exist or must be empty: a
# restore that mixes two states is not a restore.
set -eu
umask 077

usage() {
    echo "usage: restore.sh <backup-directory> <empty-target-directory>" >&2
    exit 2
}
[ "$#" -eq 2 ] || usage

source=$(cd "$1" && pwd)
target=$2

if [ -e "$target" ] && [ -n "$(ls -A "$target" 2>/dev/null)" ]; then
    echo "restore: target $target is not empty" >&2
    exit 1
fi
if [ ! -f "$source/manifest.txt" ]; then
    echo "restore: $source has no manifest.txt" >&2
    exit 1
fi
if [ ! -f "$source/contratos.db" ]; then
    echo "restore: $source has no contratos.db" >&2
    exit 1
fi

mkdir -p "$target"
python3 - "$source" "$target" <<'PY'
import hashlib
import os
import shutil
import sqlite3
import sys
from pathlib import Path

source = Path(sys.argv[1])
target = Path(sys.argv[2])

manifest = {}
for line in (source / "manifest.txt").read_text(encoding="utf-8").splitlines():
    if "=" in line:
        key, value = line.split("=", 1)
        manifest[key] = value
if manifest.get("integrity") != "ok":
    raise SystemExit("restore: the manifest does not record a clean copy")

def sha256(path):
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()

database = source / "contratos.db"
if sha256(database) != manifest.get("db_sha256"):
    raise SystemExit("restore: contratos.db does not match the manifest")
if database.stat().st_size != int(manifest.get("db_bytes", "-1")):
    raise SystemExit("restore: contratos.db has the wrong size")

check = sqlite3.connect(database)
result = check.execute("PRAGMA integrity_check").fetchone()[0]
if result != "ok":
    raise SystemExit("restore: database failed integrity_check: %s" % result)
referenced = check.execute("SELECT sha256 FROM objeto ORDER BY sha256").fetchall()
check.close()

copied = 0
for (sha,) in referenced:
    stored = source / "objects" / sha[:2] / sha
    if not stored.is_file():
        raise SystemExit("restore: referenced object %s is missing" % sha)
    if sha256(stored) != sha:
        raise SystemExit("restore: object %s does not hash to its name" % sha)
    destination = target / "objects" / sha[:2] / sha
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(stored, destination)
    if os.geteuid() == 0:
        os.chown(destination, os.stat(stored).st_uid, os.stat(stored).st_gid)
    copied += 1

if copied != int(manifest.get("objects", "-1")):
    raise SystemExit(
        "restore: copied %d objects, manifest says %s" % (copied, manifest.get("objects"))
    )

shutil.copy2(database, target / "contratos.db")
if os.geteuid() == 0:
    os.chown(target / "contratos.db", database.stat().st_uid, database.stat().st_gid)
    os.chown(target, database.stat().st_uid, database.stat().st_gid)
print(
    "restore: %d objects and %d bytes of SQLite recovered at %s"
    % (copied, (target / "contratos.db").stat().st_size, target)
)
PY

echo "restore: point the service at $target/contratos.db and $target/objects"
