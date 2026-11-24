-- CT-4: keep the original documents and their versions with provenance.

-- The immutable bytes of a document, addressed by their SHA-256. The same
-- bytes obtained from two URLs are one object; a URL that served two contents
-- is two objects.
CREATE TABLE objeto (
    sha256    TEXT    PRIMARY KEY CHECK (length(sha256) = 64),
    mime      TEXT    NOT NULL DEFAULT '',
    bytes     INTEGER NOT NULL CHECK (bytes >= 0),
    stored_at INTEGER NOT NULL
);

-- Every place an object was obtained from, and when. Two URLs serving the
-- same bytes keep both provenances; one URL serving successive contents keeps
-- one provenance per hash.
CREATE TABLE procedencia (
    id          INTEGER PRIMARY KEY,
    sha256      TEXT    NOT NULL REFERENCES objeto(sha256),
    url         TEXT    NOT NULL,
    obtained_at INTEGER NOT NULL,
    UNIQUE (sha256, url)
);

-- A document a publication version announces, tied to the fact it sustains.
-- state tells an announced link from a pending, retryable download and from
-- the evidence actually stored.
CREATE TABLE document_link (
    id             INTEGER PRIMARY KEY,
    publication_id INTEGER NOT NULL REFERENCES publication(id),
    target_kind    TEXT    NOT NULL DEFAULT 'publication'
                   CHECK (target_kind IN ('publication', 'expediente', 'lot', 'award')),
    target_ref     TEXT    NOT NULL DEFAULT '',
    url            TEXT    NOT NULL,
    title          TEXT    NOT NULL DEFAULT '',
    state          TEXT    NOT NULL DEFAULT 'announced'
                   CHECK (state IN ('announced', 'pending', 'evidence')),
    -- current evidence hash, or the hash the source announced while the bytes
    -- are still missing.
    sha256         TEXT    NOT NULL DEFAULT '',
    mime           TEXT    NOT NULL DEFAULT '',
    obtained_at    INTEGER,
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT    NOT NULL DEFAULT '',
    UNIQUE (publication_id, target_kind, target_ref, url)
);

-- Which stored objects back one document link. A URL whose bytes changed keeps
-- one evidence row per hash, so both versions stay attached to the link.
CREATE TABLE document_evidence (
    document_link_id INTEGER NOT NULL REFERENCES document_link(id),
    sha256           TEXT    NOT NULL REFERENCES objeto(sha256),
    obtained_at      INTEGER NOT NULL,
    PRIMARY KEY (document_link_id, sha256)
);

-- Old rows only said which documents a publication cited. Their hash, if any,
-- was announced by the source, never proof that the bytes were stored.
INSERT OR IGNORE INTO document_link (publication_id, url, title, state, sha256)
SELECT publication_id, url, title, 'announced', sha256
FROM document_reference
ORDER BY publication_id, url, sha256;

DROP TABLE document_reference;

CREATE INDEX document_link_publication ON document_link (publication_id);
CREATE INDEX document_link_state ON document_link (state);
CREATE INDEX procedencia_url ON procedencia (url);
