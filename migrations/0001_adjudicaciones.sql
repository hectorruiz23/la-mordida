CREATE TABLE expediente (
    id          INTEGER PRIMARY KEY,
    buyer_scheme TEXT   NOT NULL,
    buyer_id     TEXT   NOT NULL,
    folder_id    TEXT   NOT NULL,
    buyer_name   TEXT   NOT NULL DEFAULT '',
    object       TEXT   NOT NULL DEFAULT '',
    status       TEXT   NOT NULL DEFAULT '',
    UNIQUE (buyer_scheme, buyer_id, folder_id)
);

CREATE TABLE publication (
    id          INTEGER PRIMARY KEY,
    source      TEXT    NOT NULL,
    external_id TEXT    NOT NULL,
    url         TEXT    NOT NULL DEFAULT '',
    updated_at  TEXT    NOT NULL,
    body_sha256 TEXT    NOT NULL,
    imported_at INTEGER NOT NULL,
    expediente_id INTEGER NOT NULL REFERENCES expediente(id),
    UNIQUE (source, external_id, updated_at)
);

CREATE TABLE lot (
    id            INTEGER PRIMARY KEY,
    expediente_id INTEGER NOT NULL REFERENCES expediente(id),
    lot_id        TEXT    NOT NULL,
    object        TEXT    NOT NULL DEFAULT '',
    UNIQUE (expediente_id, lot_id)
);

CREATE TABLE budget_amount (
    publication_id INTEGER NOT NULL REFERENCES publication(id),
    lot_key        TEXT    NOT NULL DEFAULT '',
    tax_base       TEXT    NOT NULL CHECK (tax_base IN ('tax_exclusive', 'tax_inclusive')),
    currency       TEXT    NOT NULL DEFAULT '',
    amount_cents   INTEGER NOT NULL CHECK (amount_cents >= 0),
    PRIMARY KEY (publication_id, lot_key, tax_base)
);

CREATE TABLE award (
    id                 INTEGER PRIMARY KEY,
    publication_id     INTEGER NOT NULL REFERENCES publication(id),
    expediente_id      INTEGER NOT NULL REFERENCES expediente(id),
    position           INTEGER NOT NULL,
    lot_id             TEXT    NOT NULL DEFAULT '',
    result_code        TEXT    NOT NULL,
    award_date         TEXT    NULL CHECK (award_date IS NULL OR award_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    winner_scheme      TEXT    NOT NULL DEFAULT '',
    winner_id          TEXT    NOT NULL DEFAULT '',
    winner_name        TEXT    NOT NULL DEFAULT '',
    contract_id        TEXT    NOT NULL DEFAULT '',
    contract_issue_date TEXT   NULL CHECK (contract_issue_date IS NULL OR contract_issue_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    UNIQUE (publication_id, position)
);

CREATE TABLE award_amount (
    award_id     INTEGER NOT NULL REFERENCES award(id),
    tax_base     TEXT    NOT NULL CHECK (tax_base IN ('tax_exclusive', 'tax_inclusive')),
    currency     TEXT    NOT NULL DEFAULT '',
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    PRIMARY KEY (award_id, tax_base)
);

CREATE TABLE document_reference (
    publication_id INTEGER NOT NULL REFERENCES publication(id),
    url            TEXT    NOT NULL,
    sha256         TEXT    NOT NULL DEFAULT '',
    title          TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (publication_id, url, sha256)
);

CREATE INDEX publication_external ON publication (source, external_id, updated_at DESC);
CREATE INDEX award_expediente ON award (expediente_id, award_date);
CREATE INDEX award_publication ON award (publication_id, position);
