# Observed gaps

Measured on the October 2025 archives (a closed month when the work was done)
and on the six official samples. The counts are from the XML itself, not from
the catalogue prose. They are evidence of what an importer has to survive; none
of them is a reason to guess a value.

## Size and shape of one month

| Channel | ZIP members | Entries | Distinct `entry/id` | Uncompressed |
| --- | --- | --- | --- | --- |
| `643` profiles | 118 | 58 034 | 38 724 | 1 751 360 608 B |
| `1143` minors | 110 | 54 873 | 54 358 | 537 658 772 B |
| `1044` aggregated | 48 | 22 155 | 14 926 | 237 650 051 B |

An entry is repeated every time the expediente changes: only 38 724 of 58 034
`643` entries in the month are distinct expedientes. Importing by ZIP row would
inflate every count by roughly 50 %. The identity is `entry/id`; versions must
be kept and ordered by `entry/updated`.

## Gaps

### 1. A correction is not a notice type

The notice code list has no `CORRECCION` value. A corrected publication keeps
the original `NoticeTypeCode` and adds a second `IssueDate` under the same
`ValidNoticeInfo` and the same medium; the changed fields are not marked.

* Sample 02: `DOC_CD` and `DOC_CN` both show `2025-10-01` **and** `2025-10-17`
  under "Perfil del contratante"; the deadline moved `2025-10-16` → `2025-10-17`.
* In the October 2025 `643` archive, 11 293 (expediente, notice type, medium)
  triples carry more than one distinct `IssueDate`; `1143` has 814; `1044` has
  0. Some are corrections, some are reissues; the XML does not distinguish
  them.
* Consequence: a corrector cannot be identified from one snapshot. Keep the
  versions and treat the later `IssueDate`/`entry/updated` as the current value;
  mark "why it changed" as unknown.

### 2. The feed is a photo, not a history

`ContractFolderStatusCode` is one current value (`PRE`, `PUB`, `EV`, `ADJ`,
`RES`, `ANUL`). The path that produced it is not stored; only the accumulated
`ValidNoticeInfo` dates hint at it. The ATOM feed also caps a file at 500
entries and paginates with `next`, so a consumer that starts at the current
window sees no older publication at all.

* Sample 04 is `RES` in one snapshot; the `PUB` and `EV` states it must have had
  are not in the snapshot.
* Sample 06 has three `DOC_CN` dates and no award date; the sequence of states
  is unknown.
* Rule: a state not covered by a retained snapshot is **unknown**, never
  "never happened".

### 3. The expediente id is only unique per buyer

Spec §4.2: `ContractFolderID` is unique "respecto a su órgano". Two bodies can
publish the same string. The join key is (buyer identifier, `ContractFolderID`),
and the buyer identifier itself can be missing:

* Sample 06 has no buyer `NIF` or `DIR3`, only
  `ID_OC_PLAT=18367_18368_18369` (three concatenated ids).
* In the October `1044` archive, 8 721 awarded results have no `AwardDate`;
  they do have a `Contract/IssueDate`, which is a different event.
* Across `1044` there are 40 winner identifiers under `schemeName="OTROS"`
  (UTE or similar); in `643`, 1 127. A name is not a legal identifier.

### 4. Zero is published, and it is not a budget

A `BudgetAmount/TaxExclusiveAmount` of `0` is a real XML value but not a usable
budget. It cannot be the denominator of a rebate and it does not mean "free".

* Sample 05: `TaxExclusiveAmount=0`, `TotalAmount=0`,
  `EstimatedOverallContractAmount=90000`, award `69000` / `73830` (7 % IGIC).
* Sample 06: project budget 48 166.15, both awarded lots have no lot budget, and
  both awards publish `TaxExclusiveAmount=0`.
* Awarded results with a positive amount but a zero project budget: 328 in
  `643`, 280 in `1143`, 40 in `1044` (October 2025).
* Awarded results with no amount at all (`ResultCode=8`): 0 in `643`, 0 in
  `1143`, 185 in `1044`.
* Awarded results with no `AwardDate`: 0 in `643`, 0 in `1143`, 8 721 in `1044`.

Rule: a non-positive or absent budget excludes the row from the rebate ranking
and the reason is kept (CT-8); the award can still count in the winner ranking
(CT-7), with its amount marked unknown, not zero.

### 5. No lot breakdown in the aggregated channel

`1044` frequently publishes lots and awards with no `ProcurementProjectLot/
BudgetAmount`. In the October archive, 874 distinct expedientes have at least
one award whose lot has no budget (the ticket amounts are unpublished or
aggregated elsewhere). The project-level budget exists but cannot be split
between lots without guessing.

* Sample 06: two lots (554776, 554777), same winner, no lot budgets, awards 0.
* Rule: a lot without a breakdown is excluded from the rebate ranking with the
  reason kept (CT-8). Do not prorate the project budget.

### 6. VAT is never explicit

There is no VAT element. The rate has to be derived from
`PayableAmount − TaxExclusiveAmount`:

* 21 % in samples 01, 03 and 04; 7 % (IGIC) in sample 05.
* The ratio is not exact to six decimals because the source rounds cents:
  sample 01 gives 0.209996–0.210002. Derive the tax amount in cents, not a
  floating rate.
* When `PayableAmount` is absent, both the tax-inclusive amount and the rate are
  **unknown** (synthetic 02). Do not default to 21 %.

### 7. Currency is an attribute, and it can be absent

`currencyID` sits on every amount. When it is absent (synthetic 04), the
currency is unknown; do not assume EUR. Amounts in different currencies are not
comparable (CT-8).

### 8. Dates are of different kinds

* `AwardDate` is a civil date (`xs:date`); it must not be folded into a
  timestamp (AGENTS).
* `entry/updated` is the generation instant of the snapshot, with timezone.
* `IssueDate` is the publication date of a notice.
* `Contract/IssueDate` is the contract date, not the award date (sample 06 has
  only this one).
* `at:deleted-entry/@when` is the instant the publication disappeared.

### 9. Coverage starts are real, and per-entity coverage is not homogeneous

The catalogue states the platform-wide starts: `643` from 2012, `1044` from
2016, `1143` from 2018, `1383` from 2021-07-01, `1403` from 2022. Within those
ranges, bodies joined at different dates ("el conjunto de entidades … ha ido
creciendo progresivamente", says the `643` page). A period with no rows for a
body is therefore **unknown coverage**, not a zero result. CT-6 must record
complete/partial/gap periods per channel and body.

### 10. Minors published through aggregators are in neither channel

`1044` is explicitly *SinMenores*; `1143` only covers minors in profiles hosted
on PLACSP. A minor published through an aggregation platform is in no open-data
channel. This is a source blind spot; state it in the coverage page, do not fill
it.

### 11. Encargos and consultas are different objects

`sindicacion_1383` (encargos a medios propios) and `sindicacion_1403`
(consultas preliminares de mercado) have their own root elements and notice
codes (`DOC_FORM_ENC`, `PRO_FORM_ENC`, `DOC_CON`, …). They are not tenders and
must not enter the tender stream, even if their XML looks similar.

### 12. Annulment has no snapshot

`at:deleted-entry` with `at:comment/@type="ANULADA"` or `"CERRADA"` is a feed
event, not an entry: there is no annulled `ContractFolderStatus` to read. The
last photo is the last known state; the annulment is an event on top of it. Any
state after the last photo is unknown.

## What is *not* a gap

* The lot `ID` is present whenever lots exist; a missing `ProcurementProjectLot`
  means a single-lot/global tender, not an unknown lot.
* `ResultCode` is reliable: `8` is an award in the checked data; `3` is
  "desierto" (the non-award results in sample 06's pattern). Do not count all
  `TenderResult` elements as awards.
* `ContractFolderID` alone is enough for display; it is not enough for a join.
