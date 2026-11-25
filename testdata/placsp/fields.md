# Fields: the real element, its scope, and where it is missing

Every path below was checked against the fixtures in `official/` and against
§4 of the specification (v1.10). `cac:` is
`urn:dgpe:names:draft:codice:schema:xsd:CommonAggregateComponents-2`, `cbc:` is
`...CommonBasicComponents-2`, and the `-place-ext` variants are the PLACSP
extensions. Money is kept as the decimal string the XML carries; no `float`.

## Keys

| Purpose | Element | Scope | Notes |
| --- | --- | --- | --- |
| Publication identity | `entry/id` | publicación | Stable across versions of the same expediente. The deeplink page is `entry/link/@href`; it is not always on `contrataciondelestado.es` (sample 06 points to the Basque platform). |
| Expediente number | `ContractFolderStatus/ContractFolderID` | expediente | **Only unique per contracting body** (spec §4.2). The join key is the pair (buyer identifier, `ContractFolderID`), never the folder id alone. |
| Buyer identifier | `LocatedContractingParty/Party/PartyIdentification/ID[@schemeName]` | expediente | `NIF`, `DIR3` and `ID_PLATAFORMA` may all appear; `ID_OC_PLAT` appears in the aggregated channel. |
| Lot | `ProcurementProjectLot/ID` | lote | `@schemeName="ID_LOTE"` in current data. A single-lot or non-lotted tender has no lot element at all. |
| Award-to-lot link | `TenderResult/AwardedTenderedProject/ProcurementProjectLotID` | resultado | Empty when the award is at expediente level (samples 03, 04, 05). |
| Winner | `TenderResult/WinningParty/PartyIdentification/ID[@schemeName]` | resultado | Usually `NIF`; a UTE may be `OTROS` with no NIF, so the legal identity is unknown even though the name is published. |

Missing-key examples: sample 06 has no buyer `NIF`, only
`ID_OC_PLAT=18367_18368_18369`; its awards have no `AwardDate`; the same
expediente is published three times as `DOC_CN` with different dates. Without
the pair key, `B045-2025-00012` would collide with any other body's folder with
the same number.

## Expediente

| Field | Element | Scope | Missing / ambiguous example |
| --- | --- | --- | --- |
| Status | `ContractFolderStatusCode` (`PRE`, `PUB`, `EV`, `ADJ`, `RES`, `ANUL`) | expediente (snapshot) | Only the *current* status is present; the path that led to it is not. Sample 04 is `RES` and its older `PUB`/`EV` states are only inferable from notice dates, not stored as states. If no snapshot covers a state, it is **unknown**. |
| Object | `ProcurementProject/Name` | expediente | No missing case in the checked set; if it is absent, the object is **unknown**, never an empty string. Lot-level names are separate (`ProcurementProjectLot/ProcurementProject/Name`). |
| Buyer name | `LocatedContractingParty/Party/PartyName/Name` | expediente | Present with the identifier in the profile channel; sample 06 has a name but no NIF. Never derive the name from the identifier. |
| Contract type | `ProcurementProject/TypeCode`, `SubTypeCode` | expediente | Codes, not labels; the code lists are external `.gc` files. No missing case observed; if absent, the type is unknown, not "other". |
| CPV | `ProcurementProject/RequiredCommodityClassification/ItemClassificationCode` | expediente / lote | **Missing in sample 03** (a minor contract): no CPV element at all. Do not invent a family. |
| Budget (tax base) | `ProcurementProject/BudgetAmount/TaxExclusiveAmount` | expediente / lote | **Sample 05 has `0`**, with `EstimatedOverallContractAmount` 90 000. A zero is not a budget; it cannot be a rebate denominator. |
| Budget (with tax) | `.../BudgetAmount/TotalAmount` | expediente / lote | Absent in sample 06's lot budgets (the lot has no `BudgetAmount` at all); the project total exists. |
| Estimated value | `.../BudgetAmount/EstimatedOverallContractAmount` | expediente / lote | Not the same magnitude as the budget base (sample 05: 90 000 vs 0). Never substitute it silently. |
| Currency | `@currencyID` on each amount | amount | Absent in synthetic 04. An amount without `currencyID` has **unknown** currency; never assume EUR. |
| Procedure | `TenderingProcess/ProcedureCode`, `UrgencyCode` | expediente | Codes; not needed for the two rankings but kept for provenance. No missing case observed here; absence means unknown, not a default procedure. |

## Lote

| Field | Element | Scope | Missing / ambiguous example |
| --- | --- | --- | --- |
| Lot id | `ProcurementProjectLot/ID` | lote | No lot element for single-lot tenders (samples 03, 04, 05): the award has no `ProcurementProjectLotID` either. That is "one award", not "unknown lot". |
| Lot object | `ProcurementProjectLot/ProcurementProject/Name` | lote | Present in sample 01 (`Productos para la higiene personal`, `Productos de limpieza y pulido`). No missing case in the checked set; if absent, the lot object is unknown. |
| Lot budget | `ProcurementProjectLot/ProcurementProject/BudgetAmount/*` | lote | **Absent in sample 06**: both lots exist and are awarded, but only the project has a budget. Per-lot rebate and per-lot attribution are impossible; the reason must be kept. |
| Lot CPV | `ProcurementProjectLot/ProcurementProject/RequiredCommodityClassification/ItemClassificationCode` | lote | Sample 01 carries lot-level CPVs (`33700000`, `39800000`). **Absent in sample 06's lots** (names only): unknown CPV, not the project CPV by default. |

## Resultado (award)

| Field | Element | Scope | Missing / ambiguous example |
| --- | --- | --- | --- |
| Result code | `TenderResult/ResultCode` | resultado | `8` is an award, `3` is "desierto" in the checked data (e.g. lots of `17074914` in the October scan). Do not count non-award results. No missing case observed; an absent code is unknown, never an award. |
| Award date | `TenderResult/AwardDate` | resultado | **Missing in sample 06**. `Contract/IssueDate` (2025-09-05) is a contract date, not the award date. `AwardDate` is a civil date (`xs:date`): do not turn it into a timestamp. |
| Winner NIF | `TenderResult/WinningParty/PartyIdentification/ID[@schemeName="NIF"]` | resultado | Missing when the winner is a UTE or identified as `OTROS`; the name is still published. Do not invent an identifier. |
| Winner name | `TenderResult/WinningParty/PartyName/Name` | resultado | Present in the checked set; preserve the original spelling. The NIF can be missing while this name is present (sample 06's buyer is the mirror case: name yes, NIF no). |
| Awarded lot | `TenderResult/AwardedTenderedProject/ProcurementProjectLotID` | resultado | Empty for a global award (samples 03–05). |
| Awarded amount (tax base) | `.../LegalMonetaryTotal/TaxExclusiveAmount` | resultado | **Sample 06 publishes `0`** while the tender has a 48 166.15 budget: zero is not a usable amount. In the October 2025 aggregated archive, 185 awarded results (`ResultCode=8`) have no amount at all; `643` and `1143` had none in that month. |
| Awarded amount (with tax) | `.../LegalMonetaryTotal/PayableAmount` | resultado | Absent in sample 06's awards and in synthetic 02. Then the VAT rate and the tax-inclusive amount are **unknown**, not 0 and not 21 %. |
| Contract reference | `TenderResult/Contract/ID`, `Contract/IssueDate` | resultado | Absent in samples 01, 03 and 05 (award published before the contract). Sample 04 has contract `181746` issued 2020-10-16. |
| Award VAT | *no element* | resultado | PLACSP does not publish a VAT rate. Derive it from `PayableAmount − TaxExclusiveAmount` and round to the cent: 21 % in samples 01, 03, 04; 7 % (IGIC) in sample 05. The ratio is not exact to six decimals because the source rounds cents (sample 01: 0.209996–0.210002). |

## Publicación y evento jurídico

| Field | Element | Scope | Notes |
| --- | --- | --- | --- |
| Entry update | `entry/updated` | publicación | Timestamp with timezone; it is the moment this snapshot was generated. It is not the award date. |
| Notice type | `ValidNoticeInfo/NoticeTypeCode` | evento jurídico | `DOC_CN` (licitación), `DOC_CAN_ADJ` (adjudicación), `DOC_FORM` (formalización), `DOC_MOD` (modificación), `DOC_CD`, `DOC_PIN`, `RENUNCIA`, `DESISTIMIENTO`, etc. There is **no `CORRECCION` code**. A snapshot with an empty notice list has unknown publication history (synthetic 03 only carries `DOC_CAN_ADJ`). |
| Notice issue date | `ValidNoticeInfo/AdditionalPublicationStatus/AdditionalPublicationDocumentReference/IssueDate` | evento jurídico | May repeat for the same type and medium: a republication/correction. Sample 02: `DOC_CN` and `DOC_CD` carry 2025-10-01 **and** 2025-10-17. The spec itself warns that a medium is not a single (medium, date) pair. |
| Publication medium | `.../AdditionalPublicationStatus/PublicationMediaName` | evento jurídico | Free text (`Perfil del contratante`, `BOE`, `DOUE`, …); no closed list. |
| Documents | `AdditionalDocumentReference/Attachment/ExternalReference/URI` and `DocumentHash` | expediente | The original pliego/document links. Absent in samples 03, 05 and 06. |
| Annulment / closure | `at:deleted-entry/@ref`, `@when`, `at:comment/@type` (`ANULADA` / `CERRADA`) | publicación | Not an entry: there is no snapshot of the annulled state. The last photo remains the last known state, and the annulment is an event. |
| Legal history | *no element* | evento jurídico | There is no ordered event log. To rebuild it, sort all versions of the same `entry/id` by `entry/updated` and read their notice dates. If versions are missing, the gap is **unknown**; never infer a state from silence. |

## The rule for missing data

* A missing element is **unknown**, not zero and not a default.
* A published `0` in a budget or amount is a published zero, but it is **not a
  positive magnitude** and cannot be used as a denominator or counted as a
  free contract (sample 05, sample 06).
* `EstimatedOverallContractAmount` is not the budget base (sample 05).
* `AwardDate` is a civil date; `entry/updated` and `IssueDate` are different
  things. Only the first is the award date.
* If no notice of a type exists in the snapshot, the state that notice would
  have created is **unknown** (synthetic 03), not "did not happen".
