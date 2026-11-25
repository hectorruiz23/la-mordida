#!/bin/sh
# What the Pi actually loaded: one row per importer span, the exclusions that
# explain it, the totals per channel, the month-by-month coverage and the size
# of the two stores. It reads the database and the object tree and does not
# guess: a month with no span is missing coverage, never a measured zero.
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


def errors_of(row):
    # seen = accepted + excluded + errors, and errors are derived, not stored.
    return max(0, row["seen"] - row["publications"] - row["excluded"])


print("## Spans")
print()
print("| channel | period | kind | state | seen | accepted | excluded | errors | pages | detail |")
print("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | --- |")
spans = database.execute(
    """
    SELECT id, source, period_from, period_until, until_exclusive, kind, state,
           seen, publications, excluded, pages, last_complete_at, last_page, detail
    FROM import_span
    ORDER BY source, id
    """
).fetchall()
for row in spans:
    start = row["period_from"][:10] or "open start"
    if row["until_exclusive"]:
        end = (row["period_until"][:10] or "open end") + " (exclusive)"
    else:
        end = row["period_until"][:10] or "open end"
    print(
        "| %s | %s..%s | %s | %s | %d | %d | %d | %d | %d | %s |"
        % (
            row["source"], start, end, row["kind"], row["state"],
            row["seen"], row["publications"], row["excluded"],
            errors_of(row), row["pages"], (row["detail"] or "").replace("|", "/"),
        )
    )

print()
print("## Why units were excluded")
print()
reasons = database.execute(
    """
    SELECT source, reason, SUM(units) AS units
    FROM import_exclusion e
    JOIN import_span s ON s.id = e.span_id
    GROUP BY source, reason
    ORDER BY source, units DESC, reason
    """
).fetchall()
if not reasons:
    print("No exclusions recorded yet.")
else:
    print("| channel | reason | units |")
    print("| --- | --- | ---: |")
    for row in reasons:
        print("| %s | %s | %d |" % (row["source"], row["reason"], row["units"]))

print()
print("## Channels")
print()
print("| channel | seen | accepted | excluded | errors | last publication | last import |")
print("| --- | ---: | ---: | ---: | ---: | --- | --- |")
totals = {}
for row in spans:
    total = totals.setdefault(
        row["source"], {"seen": 0, "publications": 0, "excluded": 0, "errors": 0}
    )
    total["seen"] += row["seen"]
    total["publications"] += row["publications"]
    total["excluded"] += row["excluded"]
    total["errors"] += errors_of(row)
for source in sorted(totals):
    total = totals[source]
    last_publication = database.execute(
        "SELECT MAX(published_at) FROM publication WHERE source = ?", (source,)
    ).fetchone()[0]
    last_import = database.execute(
        "SELECT MAX(finished_at) FROM import_run WHERE source = ?", (source,)
    ).fetchone()[0]
    print(
        "| %s | %d | %d | %d | %d | %s | %s |"
        % (
            source, total["seen"], total["publications"], total["excluded"],
            total["errors"], last_publication or "unknown",
            "unknown" if not last_import else datetime.fromtimestamp(
                last_import, timezone.utc
            ).strftime("%Y-%m-%dT%H:%M:%SZ"),
        )
    )

print()
print("## Requested coverage")
print()
from_month = os.environ.get("CONTRATOS_COVERAGE_FROM", "2020-01")
until_month = os.environ.get(
    "CONTRATOS_COVERAGE_UNTIL", datetime.now(timezone.utc).strftime("%Y-%m")
)


def month_add(month, count):
    year, number = (int(part) for part in month.split("-"))
    index = year * 12 + (number - 1) + count
    return "%04d-%02d" % (index // 12, index % 12 + 1)


months = []
month = from_month
while month <= until_month:
    months.append(month)
    month = month_add(month, 1)

sources = {row["source"] for row in spans}
sources.update(
    {"placsp:sindicacion_643", "placsp:sindicacion_1044", "placsp:sindicacion_1143"}
)
print("| channel | coverage %s..%s |" % (from_month, until_month))
print("| --- | --- |")
for source in sorted(sources):
    own = [row for row in spans if row["source"] == source]
    states = []
    for month in months:
        start = month + "-01"
        end = month_add(month, 1) + "-01"
        status = "missing"
        for span in own:
            span_from = (span["period_from"] or "")[:10]
            span_until = (span["period_until"] or "")[:10] or end
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
            label = months[begin] if begin == index - 1 else "%s..%s" % (
                months[begin],
                months[index - 1],
            )
            ranges.append("%s %s" % (label, states[begin]))
            begin = index
    print("| %s | %s |" % (source, "; ".join(ranges)))

print()
print("## Gaps and partial loads")
print()
unfinished = [row for row in spans if row["state"] != "complete"]
if not unfinished:
    print("None recorded: every span in the database is complete.")
else:
    for row in unfinished:
        print(
            "- %s %s..%s %s, %d errors: %s"
            % (
                row["source"], (row["period_from"] or "")[:10] or "open start",
                (row["period_until"] or "")[:10] or "open end", row["state"],
                errors_of(row), row["detail"] or "no detail",
            )
        )

print()
print("## Reconciliation")
print()
bad = [
    row for row in spans
    if row["seen"] != row["publications"] + row["excluded"] + errors_of(row)
]
if bad:
    print("Counters do not reconcile in %d span(s):" % len(bad))
    for row in bad:
        print(
            "- %s span %d: seen %d vs accepted %d + excluded %d + errors %d"
            % (row["source"], row["id"], row["seen"], row["publications"],
               row["excluded"], errors_of(row))
        )
else:
    print("Every span satisfies seen = accepted + excluded + errors.")

database.close()

print()
print("## Size")
print()
main = Path(db_path)
wal = main.with_name(main.name + "-wal")
shm = main.with_name(main.name + "-shm")
print(
    "- SQLite: %d bytes (wal %d, shm %d)"
    % (main.stat().st_size, wal.stat().st_size if wal.exists() else 0,
       shm.stat().st_size if shm.exists() else 0)
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
