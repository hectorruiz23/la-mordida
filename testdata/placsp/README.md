# PLACSP publications — evidence before the schema

This directory holds the real publications that the importer has to read. It
was gathered for CT-1 to answer, from the source and not from guesses, what
PLACSP actually serves: which channels exist, what each one contains, at what
granularity, for which periods, and which fields are missing or ambiguous.

It is **not** production data and no importer reads it yet. CT-2 fixes the
neutral model and CT-3 writes the adapter; these fixtures are the target they
have to satisfy.

## Sources

| What | URL | Version / date | SHA-256 |
| --- | --- | --- | --- |
| Open-data catalogue (index) | https://www.hacienda.gob.es/es-es/gobiernoabierto/datos%20abiertos/paginas/licitaciones_plataforma_contratacion.aspx | retrieved 2026-11-24 | not vendored |
| Syndication and reuse specification | https://contrataciondelsectorpublico.gob.es/datosabiertos/especificacion-sindicacion.pdf | 1.10, 2024-09-17 | `e18e3aa19a1570965ca0d7fe906a54760f9ec032ae1a155fd0ecb3006b14f192` |
| Summary of the open data sets | https://contrataciondelsectorpublico.gob.es/datosabiertos/DGPE_PLACSP_ResumenDatosAbiertos.pdf | — | `84d34851df2c6e898a447188e9341eda81d836373950605163b53c830cd7a4a1` |

The two PDFs are official originals behind the channel and field statements
below. They are not committed (they are large PDFs, and the manifest carries
their URL and hash); every XML fixture *is* committed verbatim.

## Channels served by PLACSP

The catalogue publishes five data sets, each with its own ATOM feed and its own
ZIP archive naming scheme. All five were checked on the catalogue pages; three
of them have a fixture here.

| Channel id | Content | ATOM feed | Archive pattern | Coverage stated by the catalogue |
| --- | --- | --- | --- | --- |
| `sindicacion_643` | Tenders published in contracting-authority profiles hosted on PLACSP, **excluding minor contracts** | `.../sindicacion_643/licitacionesPerfilesContratanteCompleto3.atom` | `licitacionesPerfilesContratanteCompleto3_YYYY.zip` (year), `..._YYYYMM.zip` (month of the current year) | from 2012-01-01; year archives to the last closed year, monthly archives for the current year |
| `sindicacion_1044` | Tenders published through aggregation mechanisms by bodies **without** a profile on PLACSP, **excluding minor contracts** | `.../sindicacion_1044/PlataformasAgregadasSinMenores.atom` | `PlataformasAgregadasSinMenores_YYYY.zip`, `..._YYYYMM.zip` | from 2016 |
| `sindicacion_1143` | **Minor contracts** published in contracting-authority profiles hosted on PLACSP | `.../sindicacion_1143/contratosMenoresPerfilesContratantes.atom` | `contratosMenoresPerfilesContratantes_YYYY.zip`, `..._YYYYMM.zip` | from 2018 |
| `sindicacion_1383` | Encargos a medios propios (in-house assignments) | `EMP_SectorPublico.atom` | `EMP_SectorPublico_YYYY.zip` | from 2021-07-01 (the 2022 archive starts there) |
| `sindicacion_1403` | Consultas preliminares de mercado | `CPM_SectorPublico.atom` | `CPM_SectorPublico_YYYY.zip` | from 2022 |

Source of the table: the five catalogue pages linked from the index, and §2–3
of the specification. Base host: `https://contrataciondelsectorpublico.gob.es`.

### Granularity of a channel

One ATOM `<entry>` is **one publication of one expediente**, and its
`cac-place-ext:ContractFolderStatus` is the whole current snapshot of that
expediente at that moment: buyer, object, CPV, budget, every lot, every award
and every valid notice. It is not one row per lot, nor one row per award.

The feed is therefore a stream of *photos*, not a log of *events*. An
expediente published as `DOC_CN` and later awarded appears at least twice, with
the same `entry/id`, and the later entry carries the new state. The legal events
are named inside the photo by `cac-place-ext:ValidNoticeInfo` (notice type plus
issue date), so the photo and the events it has accumulated have to be told
apart. See `fields.md`.

### Overlaps and blind spots

* `643` and `1044` are **disjoint by publishing route**: a body either has its
  profile on PLACSP or publishes through an aggregator. Both exclude minor
  contracts, so there is no double counting between them for ordinary tenders.
* `1143` covers minors **only for profiles hosted on PLACSP**. A minor contract
  published through an aggregator is in `1044`? No: `1044` is explicitly
  *SinMenores*. It is in neither channel. That is a real blind spot of the
  open data, not a bug in an importer.
* `1383` and `1403` are different legal objects (encargos, market
  consultations), not tenders; they must not be mixed into the tender stream
  even if their XML shape is similar.
* The same expediente appears many times in one month's archive. Importing all
  occurrences as independent contracts multiplies the counts. The stable key is
  the publication/entry `id`; versions of it are ordered by `entry/updated`.
* In a multi-lot tender, each awarded lot is a separate `cac:TenderResult`.
  One company winning two lots counts twice in the winner ranking (CT-7); a
  single award to a UTE counts once (CT-7). A later modification is not a new
  award (CT-5).
* Coverage is not homogeneous per entity: the start year is the platform's, and
  bodies joined progressively (the catalogue says so explicitly for `643` and
  `1044`). A year with no rows for a body is *unknown coverage* for that body,
  not proof that it awarded nothing.

### How the archives are built

* The live ATOM feed returns at most 500 entries; the rest are reached through
  `atom:link[@rel="next"]`. It is the recent window, not the history.
* A monthly ZIP contains daily incremental ATOM files, not one file. The
  catalogue says to start reading at the base member
  (`licitacionesPerfilesContratanteCompleto3.atom`,
  `contratosMenoresPerfilesContratantes.atom`,
  `PlataformasAgregadasSinMenores.atom`), whose last entry links to the first
  file of the previous package.
* Monthly ZIPs exist for the current year; closed years are one ZIP per year.
  The copy of a month can be republished (HTTP `Last-Modified` changes), so the
  archive URL alone does not identify the bytes: hash them.
* `at:deleted-entry` at feed level marks a publication that disappeared from
  the platform, with `at:comment/@type` `ANULADA` or `CERRADA` (since July
  2022). It is not an entry and has no snapshot.

## Official samples

All are single-entry extracts of the official ATOM member named in
`manifest.json`; the extract keeps the original feed header and the entry bytes
unchanged. Dates are the source's, not our download date.

| # | Kind | Channel | Expediente | Entry updated | Why it is here |
| --- | --- | --- | --- | --- | --- |
| 01 | lots | 643 | `1787/2025` | 2025-10-20 | Two lots, two different winners, clean budget/award/21 % VAT; good for both rankings and for lot keys |
| 02 | correction | 643 | `37988/2025` | 2025-10-01 / 2025-10-22 | Same entry, two snapshots: deadline moved 10-16 → 10-17, notice block gained a second `IssueDate` 2025-10-17 |
| 03 | minor | 1143 | `20250145` | 2025-09-30 | Minor contract with budget 14 870 / 17 992.70 and award 14 691.62 / 17 776.86; 21 % VAT |
| 04 | before the cut | 643 | `5502` | 2025-10-21 | Initial notice issued 2019-04-10 (three `DOC_CN` dates), award 2020-06-26 and contract 2020-10-16: a post-2020 award whose initial announcement is before the 2020-01-01 cut |
| 05 | no comparable budget | 643 | `25-07/25C` | 2025-10-29 | `TaxExclusiveAmount` and `TotalAmount` are 0, `EstimatedOverallContractAmount` is 90 000, award is 69 000 / 73 830 (7 % IGIC). Not a 100 % rebate: no positive budget |
| 06 | aggregated tender | 1044 | `B045-2025-00012` | 2025-10-01 | Two lots, same winner, lot budgets absent, award amounts 0, no award date, buyer without NIF. The messy but real shape of the aggregated channel |

The hand-computed expectations are next to each fixture in `expected.json`;
`manifest.json` carries the archive URL, ZIP member, source hashes and the
derived results.

## Synthetic edge cases

These are hand-written, small, and **not official**. They exist to pin down
behaviour the official set does not contain cleanly, and are marked
`synthetic: true` in their expectations:

| # | Case | Rule it pins down |
| --- | --- | --- |
| 01 | two lots won by the same company | two awarded lots count twice; one company, two rows in the winner ranking |
| 02 | awarded amount without `PayableAmount` | the tax-inclusive amount and the VAT rate are unknown; not 21 %, not 0 |
| 03 | `ADJ` snapshot with no initial notice | the earlier states are unknown; absence is not proof the tender had no `DOC_CN` |
| 04 | amount without `currencyID` | the currency is unknown; never assume EUR |

## Layout

```
testdata/placsp/
  README.md          this file: sources, channels, granularity, coverage
  fields.md          every element used, its scope, and a missing example
  gaps.md            observed gaps and data-quality counts
  manifest.json      machine-readable inventory, hashes and computed results
  official/NN-.../   publication.atom (one-entry extract) + expected.json
  synthetic/NN-.../  synthetic publication.atom + expected.json
```

## Reproduce

The URLs in `manifest.json` are enough to re-download the exact archives.
The month used throughout is 2025-10 (the current year at collection time was
2026, so October 2025 is a closed month):

```sh
base=https://contrataciondelsectorpublico.gob.es/sindicacion
curl -O $base/sindicacion_643/licitacionesPerfilesContratanteCompleto3_202510.zip
curl -O $base/sindicacion_1044/PlataformasAgregadasSinMenores_202510.zip
curl -O $base/sindicacion_1143/contratosMenoresPerfilesContratantes_202510.zip
sha256sum *_202510.zip
```

`manifest.json` records `archive_sha256` and `zip_member_sha256`, so a
re-download can be checked byte for byte. The sample extract is the entry whose
`entry_sha256` matches. The `Last-Modified` of each archive is recorded too;
if the platform republishes the month, the hash, not the URL, is the identity.
