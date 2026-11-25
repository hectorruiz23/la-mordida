-- CT-11: the import leaves out what is not technological and keeps the count.
--
-- A publication the reader yields is seen; it either enters (publications),
-- is excluded, or fails. The unit is the expediente and, when it publishes
-- lots, each lot: an expediente with a selected lot enters and the lots that
-- do not belong to a selected family are not stored.
--
-- The counters keep the identity seen = accepted + excluded + errors. Errors
-- are derived, not stored twice: a publication that cannot be read or stored
-- is seen and is neither accepted nor excluded.

ALTER TABLE import_run ADD COLUMN seen INTEGER NOT NULL DEFAULT 0 CHECK (seen >= 0);
ALTER TABLE import_run ADD COLUMN excluded INTEGER NOT NULL DEFAULT 0 CHECK (excluded >= 0);
UPDATE import_run SET seen = publications;

ALTER TABLE import_span ADD COLUMN seen INTEGER NOT NULL DEFAULT 0 CHECK (seen >= 0);
ALTER TABLE import_span ADD COLUMN excluded INTEGER NOT NULL DEFAULT 0 CHECK (excluded >= 0);
UPDATE import_span SET seen = publications;

-- Why an excluded publication was left out, grouped by stable reason. A
-- reason with zero units is not written.
CREATE TABLE import_exclusion (
    span_id INTEGER NOT NULL REFERENCES import_span(id),
    reason  TEXT    NOT NULL,
    units   INTEGER NOT NULL CHECK (units > 0),
    PRIMARY KEY (span_id, reason)
);

CREATE INDEX import_exclusion_span ON import_exclusion (span_id);
