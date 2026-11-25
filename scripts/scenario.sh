#!/bin/sh
# The plan's acceptance scenario: serve the repository fixtures over loopback
# HTTP, import a historical range and an incremental update twice, start the
# real binary and prove both rankings, their links and their document
# fingerprints over HTTP against an independent manifest. It writes only
# inside .cache and removes its temporary database and servers on exit.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

mkdir -p .cache
tmp=$(mktemp -d .cache/scenario.XXXXXX)
fixture_server=
server=
cleanup() {
    if [ -n "$server" ]; then
        kill "$server" 2>/dev/null || true
        wait "$server" 2>/dev/null || true
    fi
    if [ -n "$fixture_server" ]; then
        kill "$fixture_server" 2>/dev/null || true
        wait "$fixture_server" 2>/dev/null || true
    fi
    rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

# A loopback file server for the fixtures: the import path reads them over
# HTTP exactly as it would read a channel.
python3 - "$root/testdata/consultas" "$tmp/fixture-port" >"$tmp/fixtures.log" 2>&1 <<'PY' &
import functools
import http.server
import pathlib
import sys

folder, port_file = sys.argv[1], sys.argv[2]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=folder)
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
pathlib.Path(port_file).write_text(str(server.server_address[1]))
server.serve_forever()
PY
fixture_server=$!

attempts=0
while [ ! -s "$tmp/fixture-port" ]; do
    if ! kill -0 "$fixture_server" 2>/dev/null; then
        cat "$tmp/fixtures.log" >&2
        echo "scenario: the fixture server stopped before it listened" >&2
        exit 1
    fi
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 100 ]; then
        cat "$tmp/fixtures.log" >&2
        echo "scenario: the fixture server did not report its port" >&2
        exit 1
    fi
    sleep 0.1
done
fixtures="http://127.0.0.1:$(cat "$tmp/fixture-port")"

db="$tmp/contratos.db"

# Historical load, incremental update, and the same update again. The second
# update must be a no-op: the store's revision identity keeps the rows equal.
./bin/contratos import -db "$db" -fixture "$fixtures/ranking.json" \
    -source consultas-ranking -from 2025-01-01 -until 2025-07-01 >"$tmp/import-ranking.log"
./bin/contratos import -db "$db" -fixture "$fixtures/ranking.json" \
    -source consultas-ranking -since-last >>"$tmp/import-ranking.log"
./bin/contratos import -db "$db" -fixture "$fixtures/ranking.json" \
    -source consultas-ranking -since-last >>"$tmp/import-ranking.log"

./bin/contratos import -db "$db" -fixture "$fixtures/rebajas.json" \
    -source consultas-rebajas -from 2025-01-01 -until 2026-01-01 >"$tmp/import-rebajas.log"
./bin/contratos import -db "$db" -fixture "$fixtures/rebajas.json" \
    -source consultas-rebajas -since-last >>"$tmp/import-rebajas.log"
./bin/contratos import -db "$db" -fixture "$fixtures/rebajas.json" \
    -source consultas-rebajas -since-last >>"$tmp/import-rebajas.log"

# The mixed fixture: one expediente with a selected and an excluded lot, one
# procedure outside the tracked families, one without any CPV, and a
# correction without CPV of the expediente already followed. Only the selected
# lot may be stored, and the counter must square.
./bin/contratos import -db "$db" -fixture "$fixtures/mixto.json" \
    -source consultas-mixto -from 2025-01-01 -until 2026-01-01 >"$tmp/import-mixto.log"

./bin/contratos serve -db "$db" -addr 127.0.0.1:0 >"$tmp/serve.log" 2>&1 &
server=$!

address=
attempts=0
while [ -z "$address" ]; do
    address=$(sed -n 's/^serve: escuchando en \([^ ]*\) .*/\1/p' "$tmp/serve.log" 2>/dev/null || true)
    if [ -n "$address" ]; then
        break
    fi
    if ! kill -0 "$server" 2>/dev/null; then
        cat "$tmp/serve.log" >&2
        echo "scenario: serve stopped before it listened" >&2
        exit 1
    fi
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 100 ]; then
        cat "$tmp/serve.log" >&2
        echo "scenario: serve did not report its address" >&2
        exit 1
    fi
    sleep 0.1
done

base="http://$address"
curl -fsS "$base/api/health" >"$tmp/health.json"
curl -fsS "$base/api/ranking?buyer_scheme=NIF&buyer_id=P1000000A&limit=100" >"$tmp/winners.json"
curl -fsS "$base/api/ranking?buyer_scheme=NIF&buyer_id=P9000000Z&limit=100" >"$tmp/winners-ute.json"
curl -fsS "$base/api/rebajas?buyer_scheme=NIF&buyer_id=P1000000A&limit=100" >"$tmp/rebates-real.json"
curl -fsS "$base/api/rebajas?buyer_scheme=NIF&buyer_id=P1000000T&limit=100" >"$tmp/rebates.json"
curl -fsS "$base/api/expedientes" >"$tmp/expedientes.json"

curl -fsS "$base/adjudicatarios?buyer_scheme=NIF&buyer_id=P1000000A&limit=100" >"$tmp/adjudicatarios.html"
curl -fsS "$base/rebajas?buyer_scheme=NIF&buyer_id=P1000000T&limit=100" >"$tmp/rebajas.html"
curl -fsS "$base/rebajas?buyer_scheme=NIF&buyer_id=P1000000T&from=2030-01-01" >"$tmp/rebajas-empty.html"
invalid_status=$(curl -s -o "$tmp/invalid.html" -w '%{http_code}' "$base/adjudicatarios?from=no-es-fecha")
curl -fsS "$base/assets/style.css" >"$tmp/style.css"

python3 - "$tmp" "$root/testdata/consultas/web-manifest.json" "$base" "$invalid_status" "$db" <<'PY'
import html
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path

tmp = Path(sys.argv[1])
manifest = json.loads(Path(sys.argv[2]).read_text(encoding="utf-8"))
base = sys.argv[3]
invalid_status = sys.argv[4]
db_path = sys.argv[5]


def load(name):
    return json.loads((tmp / name).read_text(encoding="utf-8"))


def check(condition, message):
    if not condition:
        raise SystemExit("scenario: " + message)


def get(path):
    with urllib.request.urlopen(base + html.unescape(path)) as response:
        return response.read().decode("utf-8")


def status_of(path):
    try:
        get(path)
        return 200
    except urllib.error.HTTPError as error:
        return error.code


def strip_tags(value):
    return html.unescape(re.sub(r"<[^>]+>", "", value)).strip()


def field(block, name):
    match = re.search(r'data-field="%s"[^>]*>(.*?)</td>' % re.escape(name), block, re.S)
    return strip_tags(match.group(1)) if match else None


def field_html(block, name):
    match = re.search(r'data-field="%s"[^>]*>(.*?)</td>' % re.escape(name), block, re.S)
    return match.group(1) if match else ""


def amount_of(value):
    match = re.search(r'data-amount="([^"]+)"', value)
    return match.group(1) if match else None


# --- The JSON surface still answers the same facts -------------------------
check(load("health.json") == {"ok": True}, "health does not answer ok")

winners = load("winners.json")
check(winners["total"] == 3, "winner ranking total is not 3: %r" % winners["total"])
check(len(winners["ranking"]) == 2, "winner ranking does not have two rows")
check(
    winners["ranking"][0]["wins"] == 2 and winners["ranking"][0]["percentage"] == "66.67",
    "first winner is not 2 of 3: %r" % winners["ranking"][0],
)
check(
    winners["ranking"][1]["wins"] == 1 and winners["ranking"][1]["percentage"] == "33.33",
    "second winner is not 1 of 3: %r" % winners["ranking"][1],
)
ute = load("winners-ute.json")
check(ute["total"] == 1 and ute["ranking"][0]["wins"] == 1, "the UTE is not one winner")

real = load("rebates-real.json")
check(real["total"] == 3, "real rebate total is not 3: %r" % real["total"])
real_rows = real["ranking"]
check(
    [(row["folder_id"], row.get("lot_id", "")) for row in real_rows]
    == [("RANK/2025", "2"), ("RANK/2025", "1"), ("MENOR/2025", "")],
    "real rebate order is not the expected one: %r"
    % [(row["folder_id"], row.get("lot_id", "")) for row in real_rows],
)
check(real_rows[0]["ratio"] == "0.262500", "lot 2 rebate is not 26.25 %%")
check(
    real_rows[1]["ratio"] == "0.220000" and real_rows[1]["awarded_amount"]["amount"] == "39000.00",
    "lot 1 does not use the current awarded price after the correction: %r" % real_rows[1],
)
check(real_rows[2]["ratio"] == "0.166667", "the minor rebate is not 16.67 %")

rebates = load("rebates.json")
check(rebates["total"] == 6, "rebate total is not 6: %r" % rebates["total"])
check(rebates["excluded_total"] == 9, "excluded total is not 9: %r" % rebates["excluded_total"])
rows = rebates["ranking"]
check(
    [row["folder_id"] for row in rows[:4]]
    == ["REB-20/2025", "REB-CORR/2025", "REB-TIE-A/2025", "REB-TIE-B/2025"],
    "the 20 %% group is not ordered by key: %r" % [row["folder_id"] for row in rows[:4]],
)
check([row["ratio"] for row in rows[:4]] == ["0.200000"] * 4, "the tie is not exactly 20 %%")
check(
    [row["unit_key"] for row in rows[:4]] == sorted(row["unit_key"] for row in rows[:4]),
    "the tie is not broken by the unit key",
)

by_folder = {row["folder_id"]: row for row in rows}
twenty = by_folder["REB-20/2025"]
check(twenty["initial_budget"]["amount"] == "100000.00", "the 100.000 budget is not the denominator")
check(twenty["awarded_amount"]["amount"] == "80000.00", "the 80.000 award is not the numerator")
check(twenty["percent"] == "20.0000", "100.000 to 80.000 does not render 20 %")
check(
    twenty["initial_budget_publication"]["url"] and twenty["award_publication"]["url"],
    "the row does not recover both publications",
)
check(not twenty["budget_corrected"], "an amplification is not a budget correction")

corrected = by_folder["REB-CORR/2025"]
check(corrected["initial_budget"]["amount"] == "20000.00", "the correction overwrote the original budget")
check(
    corrected["latest_budget"]["amount"] == "25000.00" and corrected["budget_corrected"] is True,
    "the correction is not preserved beside the original",
)
check(corrected["ratio"] == "0.200000", "the correction changed the initial rebate")
check(by_folder["REB-0/2025"]["ratio"] == "0.000000", "a zero rebate is not zero")
check(by_folder["REB-INCLUSIVE/2025"]["ratio"] == "0.173554", "the tax-inclusive rebate is not comparable")

expected_reasons = {
    ("REB-LOTS/2025", "1"): "no_lot_budget",
    ("REB-LOTS/2025", "2"): "no_lot_budget",
    ("REB-NOCURRENCY/2025", ""): "currency_unknown",
    ("REB-CURRENCY/2025", ""): "currency_mismatch",
    ("REB-NOBUDGET/2025", ""): "budget_missing",
    ("REB-ZEROBUDGET/2025", ""): "budget_not_positive",
    ("REB-NOAMOUNT/2025", ""): "amount_missing",
    ("REB-ZEROAMOUNT/2025", ""): "amount_not_positive",
    ("REB-TAXBASE/2025", ""): "tax_base_mismatch",
}
reasons = {
    (exclusion["folder_id"], exclusion.get("lot_id", "")): exclusion["reason"]
    for exclusion in rebates["excluded"]
}
check(reasons == expected_reasons, "exclusion reasons differ: %r" % reasons)
for exclusion in rebates["excluded"]:
    check(exclusion["detail"] and exclusion["award_publication"]["url"], "an exclusion has no evidence")

expedientes = load("expedientes.json")["expedientes"]
amplified = next(
    (expediente for expediente in expedientes if expediente["folder_id"] == "REB-20/2025"),
    None,
)
check(amplified is not None, "the amplified expediente is missing")
amplifications = [fact for fact in amplified.get("facts", []) if fact["kind"] == "amplification"]
check(amplifications, "the later amplification is not stored")
check(
    any(amount["amount"] == "10000.00" for fact in amplifications for amount in fact.get("amounts", [])),
    "the amplification amount is missing",
)

# --- The HTML readings, their links and their fingerprints -----------------
winner_page = (tmp / "adjudicatarios.html").read_text(encoding="utf-8")
winner_rows = re.findall(r'<tr data-row="winner".*?</tr>', winner_page, re.S)
check(len(winner_rows) == len(manifest["winners"]["rows"]), "winner HTML has %d rows" % len(winner_rows))
winner_awards_links = []
for block, expected in zip(winner_rows, manifest["winners"]["rows"]):
    name = re.search(r'data-field="name"[^>]*>(.*?)</a>', block, re.S)
    check(name is not None, "a winner row has no name link")
    check(strip_tags(name.group(1)) == expected["name"], "winner name %r" % strip_tags(name.group(1)))
    check(field(block, "identity") == expected["identity"], "winner identity %r" % field(block, "identity"))
    check(field(block, "wins") == expected["wins"], "winner wins %r" % field(block, "wins"))
    check(field(block, "percentage") == expected["percentage"], "winner percentage %r" % field(block, "percentage"))
    link = re.search(r'data-link="awards" href="([^"]+)"', block)
    check(link is not None, "a winner row has no awards link")
    winner_awards_links.append(link.group(1))

detail_for_folder = {}
for link, expected in zip(winner_awards_links, manifest["winners"]["rows"]):
    awards_page = get(link)
    detail_links = re.findall(r'data-link="detail" href="(/adjudicaciones/[0-9]+)"', awards_page)
    check(len(detail_links) == expected["awards"], "%s does not open %d adjudications" % (link, expected["awards"]))
    for detail_link in detail_links:
        detail = get(detail_link)
        folder = strip_tags(re.search(r"<h1>(.*?)</h1>", detail, re.S).group(1))
        detail_for_folder[folder] = detail

rebate_page = (tmp / "rebajas.html").read_text(encoding="utf-8")
rebate_rows = re.findall(r'<tr data-row="rebate".*?</tr>', rebate_page, re.S)
check(len(rebate_rows) == len(manifest["rebates"]["rows"]), "rebate HTML has %d rows" % len(rebate_rows))
for block, expected in zip(rebate_rows, manifest["rebates"]["rows"]):
    folder_link = re.search(r'data-link="detail" href="([^"]+)">(.*?)</a>', block, re.S)
    check(folder_link is not None, "a rebate row has no folder link")
    folder = strip_tags(folder_link.group(2))
    lot = field(block, "lot")
    if lot == "Global":
        lot = ""
    check(folder == expected["folder"], "rebate folder %r" % folder)
    check(lot == expected["lot"], "rebate lot %r" % lot)
    ratio = re.search(r"(\d+\.\d+) %", block)
    check(ratio is not None and ratio.group(1) == expected["ratio"], "rebate ratio %r" % (ratio.group(1) if ratio else None))
    for key in ("initial", "awarded", "difference"):
        check(amount_of(field_html(block, key)) == expected[key], "rebate %s %r" % (key, amount_of(field_html(block, key))))
    link = re.search(r'data-link="detail" href="([^"]+)"', block)
    check(link is not None, "a rebate row has no detail link")
    detail = get(link.group(1))
    folder_from_detail = strip_tags(re.search(r"<h1>(.*?)</h1>", detail, re.S).group(1))
    check(folder_from_detail == expected["folder"], "rebate detail opens %r" % folder_from_detail)
    detail_for_folder[folder_from_detail] = detail

for folder, expected in manifest["details"].items():
    check(folder in detail_for_folder, "the detail of %s was not opened from a result" % folder)
    detail = detail_for_folder[folder]
    final_html = field_html(detail, "final-amount")
    final_amount = amount_of(final_html)
    if expected["final"] is None:
        check('data-unknown="final"' in final_html, "%s does not mark the final amount unknown" % folder)
    else:
        check(final_amount == expected["final"], "%s final amount %r" % (folder, final_amount))
    documents = re.findall(
        r'data-link="document" href="([^"]+)"[^>]*data-sha256="([^"]*)"', detail
    )
    check(len(documents) == len(expected["documents"]), "%s shows %d documents" % (folder, len(documents)))
    for (url, sha), wanted in zip(documents, expected["documents"]):
        check(url == wanted["url"], "%s document url %r" % (folder, url))
        check(sha == wanted["sha256"], "%s document hash %r" % (folder, sha))
        check(re.fullmatch(r"[0-9a-f]{64}", sha), "%s document hash is not sha256" % folder)

check('data-empty="rebates"' in (tmp / "rebajas-empty.html").read_text(encoding="utf-8"), "the empty state is missing")
check(invalid_status == "400", "an invalid parameter answers %s, not 400" % invalid_status)

coverage = (tmp / "adjudicatarios.html").read_text(encoding="utf-8")
for source in manifest["coverage_sources"]:
    check(source in coverage, "coverage does not name %s" % source)
check(coverage.count("state-complete") >= 2, "coverage does not show complete loads")
check("2025-01-01 – 2025-07-01 (sin incluir)" in coverage, "coverage does not show the historical interval")
check("Actualización" in coverage, "coverage does not show the incremental load")
style = (tmp / "style.css").read_text(encoding="utf-8")
check(len(style) > 500 and "{" in style, "the embedded stylesheet did not answer as CSS")

# --- The mixed fixture: what is excluded leaves no row ---------------------
import sqlite3

database = sqlite3.connect(db_path)
database.row_factory = sqlite3.Row


def one(query, args=()):
    return database.execute(query, args).fetchone()[0]


def column(query, args=()):
    return [row[0] for row in database.execute(query, args)]


mixed = manifest["mixto"]
for folder in mixed["dropped_folders"]:
    check(one("SELECT COUNT(*) FROM expediente WHERE folder_id = ?", (folder,)) == 0,
          "excluded expediente %s was stored" % folder)
    check(one("""SELECT COUNT(*) FROM publication p
                 JOIN expediente e ON e.id = p.expediente_id WHERE e.folder_id = ?""", (folder,)) == 0,
          "excluded expediente %s kept a publication" % folder)
    check(one("""SELECT COUNT(*) FROM award a
                 JOIN expediente e ON e.id = a.expediente_id WHERE e.folder_id = ?""", (folder,)) == 0,
          "excluded expediente %s kept an award" % folder)
    check(one("""SELECT COUNT(*) FROM budget_amount b
                 JOIN publication p ON p.id = b.publication_id
                 JOIN expediente e ON e.id = p.expediente_id WHERE e.folder_id = ?""", (folder,)) == 0,
          "excluded expediente %s kept an amount" % folder)
    check(one("""SELECT COUNT(*) FROM document_link d
                 JOIN publication p ON p.id = d.publication_id
                 JOIN expediente e ON e.id = p.expediente_id WHERE e.folder_id = ?""", (folder,)) == 0,
          "excluded expediente %s kept a document link" % folder)

for folder in mixed["kept_folders"]:
    expediente = one("SELECT id FROM expediente WHERE folder_id = ?", (folder,))
    lots = column("SELECT lot_id FROM lot WHERE expediente_id = ? ORDER BY lot_id", (expediente,))
    check(lots == mixed["kept_lots"], "%s kept lots %r" % (folder, lots))
    awards = column("SELECT DISTINCT lot_id FROM award WHERE expediente_id = ? ORDER BY lot_id", (expediente,))
    check(awards == mixed["kept_lots"], "%s kept awards for lots %r" % (folder, awards))
    budget_lots = column("""SELECT DISTINCT b.lot_key FROM budget_amount b
                            JOIN publication p ON p.id = b.publication_id
                            WHERE p.expediente_id = ? ORDER BY b.lot_key""", (expediente,))
    for dropped in mixed["dropped_lots"]:
        check(dropped not in budget_lots, "%s kept the budget of excluded lot %s" % (folder, dropped))
    documents = column("""SELECT DISTINCT d.target_ref FROM document_link d
                          JOIN publication p ON p.id = d.publication_id
                          WHERE p.expediente_id = ? AND d.target_kind = 'lot'
                          ORDER BY d.target_ref""", (expediente,))
    for dropped in mixed["dropped_lots"]:
        check(dropped not in documents, "%s kept the document of excluded lot %s" % (folder, dropped))
    check(one("SELECT COUNT(*) FROM award_unit WHERE expediente_id = ?", (expediente,)) == 1,
          "%s did not count only its selected lot" % folder)
    correction = mixed["correction"]
    check(one("""SELECT COUNT(*) FROM publication p
                 WHERE p.expediente_id = ? AND p.published_at = ?""",
              (expediente, correction["published_at"])) == 1,
          "the correction without CPV of %s was not stored" % folder)
    check(one("SELECT status FROM expediente WHERE id = ?", (expediente,)) == correction["status"],
          "the correction without CPV did not project its status on %s" % folder)

section = re.search(
    r'<section class="coverage-source" data-source="%s">(.*?)</section>' % re.escape(mixed["source"]),
    coverage,
    re.S,
)
check(section is not None, "coverage does not show %s" % mixed["source"])
section = section.group(1)
row = re.search(
    r'<tr data-span="[^"]*" data-seen="(\d+)" data-accepted="(\d+)" data-excluded="(\d+)" data-errors="(\d+)">',
    section,
)
check(row is not None, "the mixed coverage row has no counters")
seen, accepted, excluded, errors = (int(value) for value in row.groups())
check(
    (seen, accepted, excluded, errors)
    == (mixed["seen"], mixed["accepted"], mixed["excluded"], mixed["errors"]),
    "mixed counters = %r" % ((seen, accepted, excluded, errors),),
)
check(seen == accepted + excluded + errors, "seen != accepted + excluded + errors")
for name, value in (("seen", seen), ("accepted", accepted), ("excluded", excluded), ("errors", errors)):
    check('data-field="%s">%d</td>' % (name, value) in section,
          "coverage does not render %s = %d" % (name, value))
reasons = {
    match.group(1): int(match.group(2))
    for match in re.finditer(r'data-exclusion-reason="([^"]+)" data-exclusion-units="(\d+)"', section)
}
check(reasons == mixed["excluded_reasons"], "exclusion reasons = %r" % reasons)
database.close()

print("scenario: both HTML rankings, the mixed exclusion, its counters and document hashes answer over the repository fixtures")
PY
