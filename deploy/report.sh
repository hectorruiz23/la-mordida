#!/bin/sh
# What the Pi actually loaded: one row per importer span, the totals per
# channel, and the size of the two stores. It reads the database and the object
# tree, it does not guess: a period with no span is not printed as zero.
set -eu

db=${CONTRATOS_DB:-/var/lib/la-mordida/contratos.db}
objects=${CONTRATOS_OBJECTS:-/var/lib/la-mordida/objects}

if [ ! -f "$db" ]; then
    echo "report: database $db does not exist" >&2
    exit 1
fi

python3 - "$db" "$objects" <<'PY'
import os
import sqlite3
import sys
from datetime import datetime, timezone
from pathlib import Path

db_path, objects_path = sys.argv[1:3]
database = sqlite3.connect(db_path)
database.row_factory = sqlite3.Row

print("## Spans")
print()
print("| channel | period | kind | state | seen | accepted | errors | pages | last whole point |")
print("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- |")
for row in database.execute(
    """
    SELECT source, period_from, period_until, until_exclusive, kind, state,
           publications, errors, pages, last_complete_at, last_page
    FROM import_span
    ORDER BY source, id
    """
):
    start = row["period_from"][:10] or "open start"
    end = row["period_until"][:10] or "open end"
    if row["until_exclusive"]:
        end = end + " (exclusive)"
    elif row["period_until"]:
        end = row["period_until"]
    point = row["last_complete_at"] or row["last_page"] or "none"
    print(
        "| %s | %s..%s | %s | %s | %d | %d | %d | %d | %s |"
        % (
            row["source"], start, end, row["kind"], row["state"],
            row["publications"] + row["errors"], row["publications"],
            row["errors"], row["pages"], point,
        )
    )

print()
print("## Channels")
print()
print("| channel | accepted | excluded (no included CPV) | errors | last publication | last import |")
print("| --- | ---: | ---: | ---: | --- | --- |")
for row in database.execute(
    """
    SELECT source,
           COUNT(*) AS accepted,
           SUM(CASE
                 WHEN EXISTS (SELECT 1 FROM cpv c WHERE c.publication_id = p.id)
                  AND NOT EXISTS (
                    SELECT 1 FROM cpv c
                    WHERE c.publication_id = p.id AND c.included = 1
                  )
                 THEN 1 ELSE 0 END) AS excluded,
           MAX(p.published_at) AS last_publication
    FROM publication p
    GROUP BY source
    ORDER BY source
    """
):
    errors = database.execute(
        "SELECT COALESCE(SUM(errors), 0) FROM import_span WHERE source = ?",
        (row["source"],),
    ).fetchone()[0]
    last_import = database.execute(
        "SELECT COALESCE(MAX(finished_at), 0) FROM import_run WHERE source = ?",
        (row["source"],),
    ).fetchone()[0]
    print(
        "| %s | %d | %d | %d | %s | %s |"
        % (
            row["source"], row["accepted"], row["excluded"], errors,
            row["last_publication"] or "unknown",
            "unknown" if not last_import else datetime.fromtimestamp(
                last_import, timezone.utc
            ).isoformat(),
        )
    )

print()
print("## Requested coverage")
print()
from_month = os.environ.get("CONTRATOS_COVERAGE_FROM", "2020-01")
until_month = os.environ.get("CONTRATOS_COVERAGE_UNTIL", datetime.now(timezone.utc).strftime("%Y-%m"))

def month_add(month, count):
    year, number = (int(part) for part in month.split("-"))
    index = year * 12 + (number - 1) + count
    return "%04d-%02d" % (index // 12, index % 12 + 1)

months = []
month = from_month
while month <= until_month:
    months.append(month)
    month = month_add(month, 1)

sources = {row["source"] for row in database.execute("SELECT DISTINCT source FROM import_span")}
sources.update({"placsp:sindicacion_643", "placsp:sindicacion_1044", "placsp:sindicacion_1143"})
print("| channel | coverage %s..%s |" % (from_month, until_month))
print("| --- | --- |")
for source in sorted(sources):
    spans = database.execute(
        "SELECT period_from, period_until, state FROM import_span WHERE source = ?",
        (source,),
    ).fetchall()
    states = []
    for month in months:
        start = month + "-01"
        end = month_add(month, 1) + "-01"
        status = "missing"
        for span in spans:
            span_from = span["period_from"][:10]
            span_until = span["period_until"][:10] or end
            if span["state"] == "complete" and (not span_from or span_from <= start) and span_until >= end:
                status = "complete"
                break
            if span_from < end and span_until > start:
                status = "partial"
        states.append(status)
    ranges = []
    begin = 0
    for index in range(1, len(states) + 1):
        if index == len(states) or states[index] != states[begin]:
            label = months[begin] if begin == index - 1 else "%s..%s" % (months[begin], months[index - 1])
            ranges.append("%s %s" % (label, states[begin]))
            begin = index
    print("| %s | %s |" % (source, "; ".join(ranges)))

print()
print("## Gaps and partial loads")
print()
rows = database.execute(
    """
    SELECT source, period_from, period_until, state, errors, detail
    FROM import_span WHERE state <> 'complete' ORDER BY source, id
    """
).fetchall()
if not rows:
    print("None recorded: every span in the database is complete.")
else:
    for row in rows:
        print(
            "- %s %s..%s %s, %d errors: %s"
            % (
                row["source"], row["period_from"][:10] or "open start",
                row["period_until"][:10] or "open end", row["state"],
                row["errors"], row["detail"] or "no detail",
            )
        )

database.close()

main = Path(db_path)
print()
print("## Size")
print()
print(
    "- SQLite: %d bytes (wal %d, shm %d)"
    % (
        main.stat().st_size,
        main.with_name(main.name + "-wal").stat().st_size if main.with_name(main.name + "-wal").exists() else 0,
        main.with_name(main.name + "-shm").stat().st_size if main.with_name(main.name + "-shm").exists() else 0,
    )
)
root = Path(objects_path)
count = size = 0
if root.is_dir():
    for current, _, files in os.walk(root):
        for name in files:
            path = Path(current) / name
            if path.name.startswith("."):
                continue
            count += 1
            size += path.stat().st_size
print("- Objects: %d files, %d bytes under %s" % (count, size, objects_path))
PY
