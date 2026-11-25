# La Mordida

Who wins the most public technology contracts in Spain, and which of them are
handed over with the deepest discount.

Two questions over the same data:
`/adjudicatarios` ranks the winners of an organism and a period, and `/rebajas`
ranks the contracts by the rebate between their initial budget and what was
finally awarded. Every row opens the awards and the original documents that
sustain it, and the page says how much of the period it actually covers.

One Go binary with SQLite: it imports publications from the public procurement
platform, keeps the originals on disk with their hashes, and serves the
rankings over what it stored.

## Build

```
make verify    # gofmt, build and tests, what CI runs
./bin/contratos --help
```

## Documents

`import` records every document a publication announces, with the fact it
sustains. `archive` downloads them into `data/objects/<prefix>/<sha256>`,
records their MIME type, URL and download date, and leaves a failed download
pending so a later run retries it. Re-running with `-refresh` downloads a
stored link again: an URL whose bytes changed keeps both versions, and the same
bytes from two URLs keep both provenances.

## History

A publication row is one source revision, identified by its source, its
external publication id, the published instant and the body hash. Replaying an
exact revision is a no-op; another body at the same instant keeps both
revisions. Two channels that publish the same photo keep two revisions but
only one victory.

The expediente (procedure), its buyer and its lots have their own identifiers;
`buyer_identity` and `expediente_identity` map the external ones. Awards and
lifecycle facts keep every revision that stated them. The current award and
the current procedure state are a projection over the newest published
revision: an older reread never rewinds it, a snapshot that omits a field never
cancels it, and two irreconcilable values at the same instant leave the disputed
value indeterminate while both revisions stay stored. An annulment is a
documented fact, never an inference from silence. Deadline compliance and
delivery are separate facts, and unknown is a value, not a zero.

## Loads and coverage

`import` serves two loads through the same use case. A historical range is
half-open on dates:

```
./bin/contratos import -db contratos.db -archive history.zip \
    -channel sindicacion_643 -from 2020-01-01 -until 2021-01-01
```

An incremental update resumes from the channel cursor, widening it by the
overlap so a boundary instant and a late revision are read again:

```
./bin/contratos import -db contratos.db -feed https://example.test/feed.atom \
    -channel sindicacion_643 -since-last
```

Every run persists `import_run` and the `import_span` it covered, with the
requested period, the counters and the last whole point. A run interrupted on
the second page keeps the first page as its resume point and the retry starts
there again; the store's revision identity makes the overlap a no-op, so a
retry ends with the same rows as a clean load. The feed is resumed at its last
whole page, never mid-page, and two entries with the same instant are
reprocessed together rather than split by the cursor.

A span is `complete` (the period was walked end to end, even when it yielded
zero publications), `partial` (whole pages were recorded before the run
stopped) or `gap` (it stopped before covering anything). `-coverage` prints
them, so a measured zero is not mistaken for missing coverage. One importer may
run at a time, enforced by the database; SQLite keeps foreign keys on, WAL on
and a bounded busy timeout, so reads continue while a load writes.

The project is in progress; see `AGENTS.md` for the rules a change has to
follow.
