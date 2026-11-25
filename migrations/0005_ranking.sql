-- CT-7: who wins the most awards.
--
-- The counting unit is one current award per result and lot. A global award
-- (no lot) is one unit too, so the key always exists: it is the procedure plus
-- the lot, and the lot is the empty string when the source publishes none.
-- Only an awarded result enters the table; a "desierto" result is not a
-- victory and a disputed projection is kept with its state instead of being
-- counted for an invented winner.

CREATE TABLE award_unit (
    unit_key            TEXT    PRIMARY KEY,
    expediente_id       INTEGER NOT NULL REFERENCES expediente(id),
    lot_id              TEXT    NOT NULL DEFAULT '',
    publication_id      INTEGER NULL REFERENCES publication(id),
    award_date          TEXT    NULL CHECK (award_date IS NULL OR award_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    result_code         TEXT    NOT NULL,
    winner_scheme       TEXT    NOT NULL DEFAULT '',
    winner_id           TEXT    NOT NULL DEFAULT '',
    winner_name         TEXT    NOT NULL DEFAULT '',
    contract_id         TEXT    NOT NULL DEFAULT '',
    contract_issue_date TEXT    NULL CHECK (contract_issue_date IS NULL OR contract_issue_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    buyer_ref           INTEGER NULL REFERENCES buyer(id),
    state               TEXT    NOT NULL CHECK (state IN ('known', 'indeterminate', 'cancelled')),
    -- One unit per procedure and lot: a repeated photo of the same award, or a
    -- correction that restates it, cannot create a second victory.
    UNIQUE (expediente_id, lot_id)
);

INSERT INTO award_unit (
    unit_key, expediente_id, lot_id, publication_id, award_date, result_code,
    winner_scheme, winner_id, winner_name, contract_id, contract_issue_date,
    buyer_ref, state
)
SELECT printf('%d:%s', ap.expediente_id, ap.lot_id), ap.expediente_id, ap.lot_id,
       ap.publication_id, ap.award_date, ap.result_code,
       ap.winner_scheme, ap.winner_id, ap.winner_name,
       ap.contract_id, ap.contract_issue_date, e.buyer_ref, ap.state
FROM award_projection ap
JOIN expediente e ON e.id = ap.expediente_id
WHERE ap.result_code = '8';

CREATE INDEX award_unit_scope ON award_unit (buyer_ref, award_date);
CREATE INDEX award_unit_winner ON award_unit (winner_scheme, winner_id);
CREATE INDEX award_unit_expediente ON award_unit (expediente_id);

-- An entity is a party the ranking can name: a company, a joint venture
-- (UTE), a group, or a party whose accredited identifier is missing. The
-- identifier is the grouping key; a name never is, so two homonyms stay two
-- entities and one entity keeps every name the source published for it.
CREATE TABLE entity (
    id         INTEGER PRIMARY KEY,
    kind       TEXT    NOT NULL DEFAULT 'company'
               CHECK (kind IN ('company', 'ute', 'group', 'unidentified')),
    legal_name TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE entity_identity (
    entity_id   INTEGER NOT NULL REFERENCES entity(id),
    scheme      TEXT    NOT NULL,
    external_id TEXT    NOT NULL,
    source      TEXT    NOT NULL DEFAULT '',
    valid_from  TEXT    NOT NULL DEFAULT '' CHECK (valid_from = '' OR valid_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    valid_to    TEXT    NOT NULL DEFAULT '' CHECK (valid_to = '' OR valid_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    UNIQUE (scheme, external_id)
);

CREATE TABLE entity_name (
    entity_id INTEGER NOT NULL REFERENCES entity(id),
    name      TEXT    NOT NULL,
    source    TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (entity_id, name)
);

-- A member of a group or a UTE. The percentage is the one the source
-- published, kept in basis points; it is never used to split an amount that
-- the source did not split. One edge per source and validity start, so a
-- repeated import cannot multiply a member through the join.
CREATE TABLE entity_member (
    group_id       INTEGER NOT NULL REFERENCES entity(id),
    member_id      INTEGER NOT NULL REFERENCES entity(id),
    source         TEXT    NOT NULL,
    valid_from     TEXT    NOT NULL DEFAULT '' CHECK (valid_from = '' OR valid_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    valid_to       TEXT    NOT NULL DEFAULT '' CHECK (valid_to = '' OR valid_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    percentage_bps INTEGER NULL CHECK (percentage_bps IS NULL OR percentage_bps BETWEEN 0 AND 10000),
    PRIMARY KEY (group_id, member_id, source, valid_from)
);

CREATE INDEX entity_member_member ON entity_member (member_id);

-- The dated link between an entity and the group or legal holder it belongs
-- to, when that relationship is documented.
CREATE TABLE entity_group (
    entity_id  INTEGER NOT NULL REFERENCES entity(id),
    group_id   INTEGER NOT NULL REFERENCES entity(id),
    relation   TEXT    NOT NULL DEFAULT 'group'
               CHECK (relation IN ('group', 'legal_holder')),
    source     TEXT    NOT NULL,
    valid_from TEXT    NOT NULL DEFAULT '' CHECK (valid_from = '' OR valid_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    valid_to   TEXT    NOT NULL DEFAULT '' CHECK (valid_to = '' OR valid_to GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    PRIMARY KEY (entity_id, group_id, relation, source, valid_from)
);

CREATE INDEX entity_group_group ON entity_group (group_id);
