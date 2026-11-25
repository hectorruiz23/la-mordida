-- CT-6: resumable loads and coverage by period.
--
-- A run is one invocation of the importer over one channel and one period. A
-- span is the period that run actually covered, with its counters and its
-- last whole page. complete means the period was walked end to end: zero
-- publications on a complete span is a measured zero, not missing coverage.
-- partial means some whole pages were recorded and the run stopped; gap means
-- it stopped before covering anything. The cursor is the last whole point the
-- channel reached, kept per source so a historical and an incremental load
-- share the same resume point.
--
-- Only one importer may be running at a time: the partial unique index makes
-- the database, not the caller, enforce it.

CREATE TABLE import_run (
    id              INTEGER PRIMARY KEY,
    source          TEXT    NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('historical', 'incremental')),
    requested_from  TEXT    NOT NULL DEFAULT '',
    requested_until TEXT    NOT NULL DEFAULT '',
    period_from     TEXT    NOT NULL DEFAULT '',
    period_until    TEXT    NOT NULL DEFAULT '',
    until_exclusive INTEGER NOT NULL DEFAULT 0 CHECK (until_exclusive IN (0, 1)),
    state           TEXT    NOT NULL CHECK (state IN ('running', 'completed', 'partial', 'failed')),
    started_at      INTEGER NOT NULL,
    finished_at     INTEGER NULL,
    publications    INTEGER NOT NULL DEFAULT 0,
    pages           INTEGER NOT NULL DEFAULT 0,
    detail          TEXT    NOT NULL DEFAULT ''
);

CREATE UNIQUE INDEX import_run_one_active ON import_run (state) WHERE state = 'running';
CREATE INDEX import_run_source ON import_run (source, started_at);

CREATE TABLE import_span (
    id              INTEGER PRIMARY KEY,
    run_id          INTEGER NOT NULL REFERENCES import_run(id),
    source          TEXT    NOT NULL,
    kind            TEXT    NOT NULL CHECK (kind IN ('historical', 'incremental')),
    period_from     TEXT    NOT NULL DEFAULT '',
    period_until    TEXT    NOT NULL DEFAULT '',
    until_exclusive INTEGER NOT NULL DEFAULT 0 CHECK (until_exclusive IN (0, 1)),
    state           TEXT    NOT NULL CHECK (state IN ('complete', 'partial', 'gap')),
    publications    INTEGER NOT NULL DEFAULT 0,
    pages           INTEGER NOT NULL DEFAULT 0,
    last_complete_at TEXT   NOT NULL DEFAULT '',
    last_page       TEXT    NOT NULL DEFAULT '',
    detail          TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX import_span_source ON import_span (source, id);

CREATE TABLE import_cursor (
    source           TEXT    PRIMARY KEY,
    last_complete_at TEXT    NOT NULL DEFAULT '',
    last_page        TEXT    NOT NULL DEFAULT '',
    updated_at       INTEGER NOT NULL
);
