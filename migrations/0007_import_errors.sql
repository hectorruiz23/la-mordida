-- CT-10: a published entry that cannot be represented without guessing is
-- counted and its reason kept, instead of aborting the whole page. The count is
-- per run and per span; the detail keeps the first reasons, capped in the app.

ALTER TABLE import_run ADD COLUMN errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_run ADD COLUMN error_detail TEXT NOT NULL DEFAULT '';

ALTER TABLE import_span ADD COLUMN errors INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_span ADD COLUMN error_detail TEXT NOT NULL DEFAULT '';
