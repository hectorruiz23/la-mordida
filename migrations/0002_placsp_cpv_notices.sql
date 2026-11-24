-- CPV selection and legal notices are part of a publication version. Both are
-- versioned with it: a correction that drops the CPV does not erase what an
-- earlier version published.

CREATE TABLE classification (
    publication_id INTEGER NOT NULL REFERENCES publication(id),
    scope          TEXT    NOT NULL CHECK (scope IN ('expediente', 'lote')),
    lot_key        TEXT    NOT NULL DEFAULT '',
    source         TEXT    NOT NULL DEFAULT '',
    ambiguous      INTEGER NOT NULL DEFAULT 0 CHECK (ambiguous IN (0, 1)),
    reason         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (publication_id, scope, lot_key)
);

CREATE TABLE cpv (
    publication_id INTEGER NOT NULL REFERENCES publication(id),
    scope          TEXT    NOT NULL CHECK (scope IN ('expediente', 'lote')),
    lot_key        TEXT    NOT NULL DEFAULT '',
    position       INTEGER NOT NULL,
    code           TEXT    NOT NULL,
    family         TEXT    NOT NULL DEFAULT '',
    included       INTEGER NOT NULL CHECK (included IN (0, 1)),
    reason         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (publication_id, scope, lot_key, position)
);

CREATE TABLE notice (
    publication_id INTEGER NOT NULL REFERENCES publication(id),
    position       INTEGER NOT NULL,
    notice_type    TEXT    NOT NULL,
    medium         TEXT    NOT NULL DEFAULT '',
    issue_index    INTEGER NOT NULL,
    issue_date     TEXT    NULL CHECK (issue_date IS NULL OR issue_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    PRIMARY KEY (publication_id, position, issue_index)
);
