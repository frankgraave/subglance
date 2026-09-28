-- 0021_ntfy_gotify_channels.sql — accept ntfy and Gotify as channel types.
--
-- +subglance defer-foreign-keys
--
-- Both are self-hostable push services: the way people who run their own
-- infrastructure get an alert onto a phone without an account on a third-party
-- chat platform. Neither adds a column; each is a type plus a config map, the
-- same shape as the five types before them.
--
-- SQLite cannot widen a CHECK constraint in place, so the table is rebuilt by
-- the documented twelve-step recipe. The directive above makes the migration
-- runner defer foreign keys for the duration: removing notif_channels with them
-- enforced would cascade away every monitor assignment, outbox row, quiet-hours
-- window, maintenance recipient and routing rule that points at a channel.
--
-- config_json is copied byte for byte, so a database encrypted with
-- --secret-key stays encrypted and needs no key to migrate.

CREATE TABLE notif_channels_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    type        TEXT    NOT NULL
                        CHECK (type IN ('webhook', 'discord', 'slack', 'telegram', 'email',
                                        'ntfy', 'gotify')),
    config_json TEXT    NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    is_default  INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1))
) STRICT;

INSERT INTO notif_channels_new
    (id, name, type, config_json, enabled, created_at, updated_at, is_default)
SELECT id, name, type, config_json, enabled, created_at, updated_at, is_default
FROM notif_channels;

-- AUTOINCREMENT promises that an id is never handed out twice, including the
-- id of a channel that has since been deleted. The copy above only teaches the
-- new table the highest id still present, so carry the old high-water mark
-- across before the old table (and its sqlite_sequence row) goes away.
UPDATE sqlite_sequence
   SET seq = max(seq, coalesce((SELECT seq FROM sqlite_sequence
                                 WHERE name = 'notif_channels'), 0))
 WHERE name = 'notif_channels_new';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'notif_channels_new', seq FROM sqlite_sequence
 WHERE name = 'notif_channels'
   AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'notif_channels_new');

DROP TABLE notif_channels;
ALTER TABLE notif_channels_new RENAME TO notif_channels;

CREATE UNIQUE INDEX notif_channels_one_default
    ON notif_channels (is_default) WHERE is_default = 1;
