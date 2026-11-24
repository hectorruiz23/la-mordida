-- CT-5: rebuild the procedure, its revisions and its current projection.
--
-- A publication row is one source revision. Its identity is the source, the
-- external publication id, the published instant and the body hash: the same
-- photo arriving through two channels is two revisions, the same instant with
-- other bytes keeps both revisions, and an exact replay is a no-op.
--
-- The expediente is the internal procedure, the buyer is an internal party and
-- the identity tables map the source's external identifiers to them. The
-- current state lives in projections; the historical facts keep every version
-- so a missing field is never read as a cancellation and a numeric difference
-- is never given an invented reason.

PRAGMA defer_foreign_keys = ON;

-- One revision per source snapshot. published_at is the source's updated
-- instant, effective_date is the civil date the revision applies from when the
-- source publishes one, observed_at is when this importer first saw it.
-- The body hash is part of the identity: two irreconcilable snapshots of the
-- same source and instant are both kept, never overwritten.
CREATE TABLE publication_new (
    id             INTEGER PRIMARY KEY,
    source         TEXT    NOT NULL,
    external_id    TEXT    NOT NULL,
    url            TEXT    NOT NULL DEFAULT '',
    published_at   TEXT    NOT NULL,
    effective_date TEXT    NULL CHECK (effective_date IS NULL OR effective_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    observed_at    INTEGER NOT NULL,
    body_sha256    TEXT    NOT NULL,
    object         TEXT    NOT NULL DEFAULT '',
    status         TEXT    NOT NULL DEFAULT '',
    expediente_id  INTEGER NOT NULL REFERENCES expediente(id),
    UNIQUE (source, external_id, published_at, body_sha256)
);

INSERT INTO publication_new (
    id, source, external_id, url, published_at, effective_date, observed_at,
    body_sha256, object, status, expediente_id
)
SELECT p.id, p.source, p.external_id, p.url, p.updated_at, NULL, p.imported_at,
       p.body_sha256, e.object, e.status, p.expediente_id
FROM publication p
JOIN expediente e ON e.id = p.expediente_id;

CREATE TABLE budget_amount_new (
    publication_id INTEGER NOT NULL REFERENCES publication_new(id),
    lot_key        TEXT    NOT NULL DEFAULT '',
    tax_base       TEXT    NOT NULL CHECK (tax_base IN ('tax_exclusive', 'tax_inclusive')),
    currency       TEXT    NOT NULL DEFAULT '',
    amount_cents   INTEGER NOT NULL CHECK (amount_cents >= 0),
    PRIMARY KEY (publication_id, lot_key, tax_base)
);

INSERT INTO budget_amount_new (publication_id, lot_key, tax_base, currency, amount_cents)
SELECT publication_id, lot_key, tax_base, currency, amount_cents FROM budget_amount;

CREATE TABLE award_new (
    id                  INTEGER PRIMARY KEY,
    publication_id      INTEGER NOT NULL REFERENCES publication_new(id),
    expediente_id       INTEGER NOT NULL REFERENCES expediente(id),
    position            INTEGER NOT NULL,
    lot_id              TEXT    NOT NULL DEFAULT '',
    result_code         TEXT    NOT NULL,
    award_date          TEXT    NULL CHECK (award_date IS NULL OR award_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    winner_scheme       TEXT    NOT NULL DEFAULT '',
    winner_id           TEXT    NOT NULL DEFAULT '',
    winner_name         TEXT    NOT NULL DEFAULT '',
    contract_id         TEXT    NOT NULL DEFAULT '',
    contract_issue_date TEXT    NULL CHECK (contract_issue_date IS NULL OR contract_issue_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    UNIQUE (publication_id, position)
);

INSERT INTO award_new (
    id, publication_id, expediente_id, position, lot_id, result_code, award_date,
    winner_scheme, winner_id, winner_name, contract_id, contract_issue_date
)
SELECT id, publication_id, expediente_id, position, lot_id, result_code, award_date,
       winner_scheme, winner_id, winner_name, contract_id, contract_issue_date
FROM award;

CREATE TABLE award_amount_new (
    award_id     INTEGER NOT NULL REFERENCES award_new(id),
    tax_base     TEXT    NOT NULL CHECK (tax_base IN ('tax_exclusive', 'tax_inclusive')),
    currency     TEXT    NOT NULL DEFAULT '',
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    PRIMARY KEY (award_id, tax_base)
);

INSERT INTO award_amount_new (award_id, tax_base, currency, amount_cents)
SELECT award_id, tax_base, currency, amount_cents FROM award_amount;

CREATE TABLE classification_new (
    publication_id INTEGER NOT NULL REFERENCES publication_new(id),
    scope          TEXT    NOT NULL CHECK (scope IN ('expediente', 'lote')),
    lot_key        TEXT    NOT NULL DEFAULT '',
    source         TEXT    NOT NULL DEFAULT '',
    ambiguous      INTEGER NOT NULL DEFAULT 0 CHECK (ambiguous IN (0, 1)),
    reason         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (publication_id, scope, lot_key)
);

INSERT INTO classification_new (publication_id, scope, lot_key, source, ambiguous, reason)
SELECT publication_id, scope, lot_key, source, ambiguous, reason FROM classification;

CREATE TABLE cpv_new (
    publication_id INTEGER NOT NULL REFERENCES publication_new(id),
    scope          TEXT    NOT NULL CHECK (scope IN ('expediente', 'lote')),
    lot_key        TEXT    NOT NULL DEFAULT '',
    position       INTEGER NOT NULL,
    code           TEXT    NOT NULL,
    family         TEXT    NOT NULL DEFAULT '',
    included       INTEGER NOT NULL CHECK (included IN (0, 1)),
    reason         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (publication_id, scope, lot_key, position)
);

INSERT INTO cpv_new (publication_id, scope, lot_key, position, code, family, included, reason)
SELECT publication_id, scope, lot_key, position, code, family, included, reason FROM cpv;

CREATE TABLE notice_new (
    publication_id INTEGER NOT NULL REFERENCES publication_new(id),
    position       INTEGER NOT NULL,
    notice_type    TEXT    NOT NULL,
    medium         TEXT    NOT NULL DEFAULT '',
    issue_index    INTEGER NOT NULL,
    issue_date     TEXT    NULL CHECK (issue_date IS NULL OR issue_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    PRIMARY KEY (publication_id, position, issue_index)
);

INSERT INTO notice_new (publication_id, position, notice_type, medium, issue_index, issue_date)
SELECT publication_id, position, notice_type, medium, issue_index, issue_date FROM notice;

CREATE TABLE document_link_new (
    id             INTEGER PRIMARY KEY,
    publication_id INTEGER NOT NULL REFERENCES publication_new(id),
    target_kind    TEXT    NOT NULL DEFAULT 'publication'
                   CHECK (target_kind IN ('publication', 'expediente', 'lot', 'award')),
    target_ref     TEXT    NOT NULL DEFAULT '',
    url            TEXT    NOT NULL,
    title          TEXT    NOT NULL DEFAULT '',
    state          TEXT    NOT NULL DEFAULT 'announced'
                   CHECK (state IN ('announced', 'pending', 'evidence')),
    sha256         TEXT    NOT NULL DEFAULT '',
    mime           TEXT    NOT NULL DEFAULT '',
    obtained_at    INTEGER,
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT    NOT NULL DEFAULT '',
    UNIQUE (publication_id, target_kind, target_ref, url)
);

INSERT INTO document_link_new (
    id, publication_id, target_kind, target_ref, url, title, state, sha256,
    mime, obtained_at, attempts, last_error
)
SELECT id, publication_id, target_kind, target_ref, url, title, state, sha256,
       mime, obtained_at, attempts, last_error
FROM document_link;

CREATE TABLE document_evidence_new (
    document_link_id INTEGER NOT NULL REFERENCES document_link_new(id),
    sha256           TEXT    NOT NULL REFERENCES objeto(sha256),
    obtained_at      INTEGER NOT NULL,
    PRIMARY KEY (document_link_id, sha256)
);

INSERT INTO document_evidence_new (document_link_id, sha256, obtained_at)
SELECT document_link_id, sha256, obtained_at FROM document_evidence;

DROP TABLE document_evidence;
DROP TABLE document_link;
DROP TABLE notice;
DROP TABLE cpv;
DROP TABLE classification;
DROP TABLE award_amount;
DROP TABLE award;
DROP TABLE budget_amount;
DROP TABLE publication;

ALTER TABLE publication_new RENAME TO publication;
ALTER TABLE budget_amount_new RENAME TO budget_amount;
ALTER TABLE award_new RENAME TO award;
ALTER TABLE award_amount_new RENAME TO award_amount;
ALTER TABLE classification_new RENAME TO classification;
ALTER TABLE cpv_new RENAME TO cpv;
ALTER TABLE notice_new RENAME TO notice;
ALTER TABLE document_link_new RENAME TO document_link;
ALTER TABLE document_evidence_new RENAME TO document_evidence;

CREATE INDEX publication_external ON publication (source, external_id, published_at DESC, id DESC);
CREATE INDEX publication_current ON publication (expediente_id, published_at DESC, id DESC);
CREATE INDEX award_expediente ON award (expediente_id, lot_id, award_date);
CREATE INDEX award_publication ON award (publication_id, position);
CREATE INDEX document_link_publication ON document_link (publication_id);
CREATE INDEX document_link_state ON document_link (state);

-- The buyer is an internal party; its external identifiers live beside it.
-- One buyer may publish several schemes (NIF, DIR3, platform id), and the
-- folder number alone never identifies it.
CREATE TABLE buyer (
    id   INTEGER PRIMARY KEY,
    name TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE buyer_identity (
    buyer_id    INTEGER NOT NULL REFERENCES buyer(id),
    scheme      TEXT    NOT NULL,
    external_id TEXT    NOT NULL,
    source      TEXT    NOT NULL DEFAULT '',
    first_seen  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL,
    UNIQUE (scheme, external_id)
);

ALTER TABLE expediente ADD COLUMN buyer_ref INTEGER NULL REFERENCES buyer(id);

-- Carry the buyers that already had expedientes. The internal buyer id is the
-- smallest expediente id of the group; the next new buyer takes the next free
-- id. A new source identifier becomes a new buyer: merging two identifiers is
-- an explicit act, never an inference from a name.
INSERT INTO buyer (id, name)
SELECT MIN(id), MAX(buyer_name) FROM expediente GROUP BY buyer_scheme, buyer_id;

INSERT INTO buyer_identity (buyer_id, scheme, external_id, source, first_seen, last_seen)
SELECT b.id, e.buyer_scheme, e.buyer_id, '', 0, 0
FROM expediente e
JOIN buyer b ON b.id = (
    SELECT MIN(e2.id) FROM expediente e2
    WHERE e2.buyer_scheme = e.buyer_scheme AND e2.buyer_id = e.buyer_id
)
GROUP BY e.buyer_scheme, e.buyer_id;

UPDATE expediente
SET buyer_ref = (
    SELECT b.id FROM buyer b
    JOIN buyer_identity bi ON bi.buyer_id = b.id
    WHERE bi.scheme = expediente.buyer_scheme AND bi.external_id = expediente.buyer_id
);

-- The source publication id maps to the internal procedure. Two channels may
-- publish the same procedure, and two buyers may use the same folder id: the
-- mapping is by the pair, never by the folder number alone.
CREATE TABLE expediente_identity (
    expediente_id INTEGER NOT NULL REFERENCES expediente(id),
    source        TEXT    NOT NULL,
    external_id   TEXT    NOT NULL,
    first_seen    INTEGER NOT NULL,
    last_seen     INTEGER NOT NULL,
    UNIQUE (source, external_id)
);

INSERT INTO expediente_identity (expediente_id, source, external_id, first_seen, last_seen)
SELECT p.expediente_id, p.source, p.external_id, MIN(p.observed_at), MAX(p.observed_at)
FROM publication p
GROUP BY p.expediente_id, p.source, p.external_id;

ALTER TABLE expediente ADD COLUMN current_publication_id INTEGER NULL REFERENCES publication(id);
ALTER TABLE expediente ADD COLUMN projection_state TEXT NOT NULL DEFAULT 'known'
    CHECK (projection_state IN ('known', 'indeterminate', 'annulled'));

UPDATE expediente
SET current_publication_id = (
    SELECT p.id FROM publication p
    WHERE p.expediente_id = expediente.id
    ORDER BY julianday(p.published_at) DESC, p.id DESC
    LIMIT 1
);

CREATE INDEX expediente_buyer_folder ON expediente (buyer_ref, folder_id);
CREATE INDEX expediente_identity_expediente ON expediente_identity (expediente_id);

ALTER TABLE lot ADD COLUMN current_publication_id INTEGER NULL REFERENCES publication(id);
ALTER TABLE lot ADD COLUMN projection_state TEXT NOT NULL DEFAULT 'known'
    CHECK (projection_state IN ('known', 'indeterminate', 'annulled'));

-- Lifecycle facts are the legal events around an award. The award itself is
-- not duplicated here: its typed result (winner, amounts, contract) lives in
-- award. The kind is explicit evidence; the source does not let us derive
-- "rectification" from a numeric difference, so no kind is invented. Deadline
-- compliance and delivery are separate columns: one unknown never fills the
-- other.
CREATE TABLE fact (
    id              INTEGER PRIMARY KEY,
    publication_id  INTEGER NOT NULL REFERENCES publication(id),
    expediente_id   INTEGER NOT NULL REFERENCES expediente(id),
    lot_id          TEXT    NOT NULL DEFAULT '',
    kind            TEXT    NOT NULL CHECK (kind IN (
                        'rectification',
                        'extension_planned',
                        'extension_exercised',
                        'amplification',
                        'scope_change',
                        'reception',
                        'final_payment',
                        'annulment')),
    external_ref    TEXT    NOT NULL DEFAULT '',
    effective_date  TEXT    NULL CHECK (effective_date IS NULL OR effective_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    deadline_status TEXT    NOT NULL DEFAULT 'unknown'
                    CHECK (deadline_status IN ('unknown', 'on_time', 'late')),
    delivery_status TEXT    NOT NULL DEFAULT 'unknown'
                    CHECK (delivery_status IN ('unknown', 'delivered', 'not_delivered')),
    detail          TEXT    NOT NULL DEFAULT '',
    evidence_sha256 TEXT    NOT NULL DEFAULT '',
    UNIQUE (publication_id, kind, external_ref, lot_id)
);

CREATE TABLE fact_amount (
    fact_id      INTEGER NOT NULL REFERENCES fact(id),
    tax_base     TEXT    NOT NULL CHECK (tax_base IN ('tax_exclusive', 'tax_inclusive')),
    currency     TEXT    NOT NULL DEFAULT '',
    amount_cents INTEGER NOT NULL CHECK (amount_cents >= 0),
    PRIMARY KEY (fact_id, tax_base)
);

CREATE INDEX fact_expediente ON fact (expediente_id, effective_date, id);
CREATE INDEX fact_publication ON fact (publication_id);

-- The current award of every procedure and lot. It points at the source
-- revision that sustains it. A same-instant conflict marks the projection
-- indeterminate and the disputed amounts are absent; both award rows stay in
-- the history above. An annulment is only ever an explicit fact, never the
-- silence of a later snapshot.
CREATE TABLE award_projection (
    expediente_id       INTEGER NOT NULL REFERENCES expediente(id),
    lot_id              TEXT    NOT NULL DEFAULT '',
    state               TEXT    NOT NULL CHECK (state IN ('known', 'indeterminate', 'cancelled')),
    publication_id      INTEGER NULL REFERENCES publication(id),
    result_code         TEXT    NOT NULL DEFAULT '',
    award_date          TEXT    NULL CHECK (award_date IS NULL OR award_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    winner_scheme       TEXT    NOT NULL DEFAULT '',
    winner_id           TEXT    NOT NULL DEFAULT '',
    winner_name         TEXT    NOT NULL DEFAULT '',
    contract_id         TEXT    NOT NULL DEFAULT '',
    contract_issue_date TEXT    NULL CHECK (contract_issue_date IS NULL OR contract_issue_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    PRIMARY KEY (expediente_id, lot_id)
);

CREATE TABLE award_projection_amount (
    expediente_id INTEGER NOT NULL,
    lot_id        TEXT    NOT NULL DEFAULT '',
    tax_base      TEXT    NOT NULL CHECK (tax_base IN ('tax_exclusive', 'tax_inclusive')),
    currency      TEXT    NOT NULL DEFAULT '',
    amount_cents  INTEGER NOT NULL CHECK (amount_cents >= 0),
    PRIMARY KEY (expediente_id, lot_id, tax_base),
    FOREIGN KEY (expediente_id, lot_id) REFERENCES award_projection(expediente_id, lot_id) ON DELETE CASCADE
);

CREATE INDEX award_projection_publication ON award_projection (publication_id);

-- Carry the current award over for databases that already had data. This is a
-- first projection from the latest revision; the next import recomputes it.
CREATE TEMP TABLE migration_latest_award AS
SELECT a.expediente_id, a.lot_id, MAX(a.id) AS award_id
FROM award a
JOIN publication p ON p.id = a.publication_id
WHERE p.id = (
    SELECT p2.id FROM publication p2
    WHERE p2.expediente_id = a.expediente_id
    ORDER BY julianday(p2.published_at) DESC, p2.id DESC
    LIMIT 1
)
GROUP BY a.expediente_id, a.lot_id;

INSERT INTO award_projection (
    expediente_id, lot_id, state, publication_id, result_code, award_date,
    winner_scheme, winner_id, winner_name, contract_id, contract_issue_date
)
SELECT a.expediente_id, a.lot_id, 'known', a.publication_id, a.result_code, a.award_date,
       a.winner_scheme, a.winner_id, a.winner_name, a.contract_id, a.contract_issue_date
FROM award a
JOIN migration_latest_award m ON m.award_id = a.id;

INSERT INTO award_projection_amount (expediente_id, lot_id, tax_base, currency, amount_cents)
SELECT a.expediente_id, a.lot_id, am.tax_base, am.currency, am.amount_cents
FROM award_amount am
JOIN award a ON a.id = am.award_id
JOIN migration_latest_award m ON m.award_id = a.id;

DROP TABLE migration_latest_award;
