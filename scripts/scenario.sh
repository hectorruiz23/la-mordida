#!/bin/sh
# The plan's acceptance scenario: import the repository fixtures, start the
# real binary and prove both rankings over HTTP. It writes only inside .cache
# and removes its temporary database and server on exit.
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

mkdir -p .cache
tmp=$(mktemp -d .cache/scenario.XXXXXX)
server=
cleanup() {
    if [ -n "$server" ]; then
        kill "$server" 2>/dev/null || true
        wait "$server" 2>/dev/null || true
    fi
    rm -rf "$tmp"
}
trap cleanup EXIT INT TERM

db="$tmp/contratos.db"
./bin/contratos import -db "$db" -fixture testdata/consultas/ranking.json >"$tmp/import-ranking.log"
./bin/contratos import -db "$db" -fixture testdata/consultas/rebajas.json >"$tmp/import-rebajas.log"

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

python3 - "$tmp" <<'PY'
import json
import sys
from pathlib import Path

tmp = Path(sys.argv[1])


def load(name):
    return json.loads((tmp / name).read_text(encoding="utf-8"))


def check(condition, message):
    if not condition:
        raise SystemExit("scenario: " + message)


check(load("health.json") == {"ok": True}, "health does not answer ok")

# Ranking of winners: two lots and a global award under the same buyer.
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

# Rebates over the same fixture: the corrected award price is the current one
# and each lot uses its own budget, not the project budget.
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

# The tabular fixture: 20 %, 0 %, tie, global budget of several lots and a
# budget correction.
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

# The later amplification is stored and did not become a second award or a
# different denominator.
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

print("scenario: both rankings answer over the repository fixtures")
PY
