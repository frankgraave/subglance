-- 0024_sms_channels.sql — accept sms as a channel type.
--
-- +subglance defer-foreign-keys
--
-- One type for text messages, with the provider (an Android phone running SMS
-- Gateway for Android, or Twilio) as a setting in config_json. No column is
-- added.
--
-- Same rebuild as 0021, for the same reason: SQLite cannot widen a CHECK
-- constraint in place. The directive above defers foreign keys while the old
-- table is dropped, so nothing that points at a channel cascades away, and
-- config_json is copied byte for byte, so encrypted settings stay encrypted.
-- The channel_config_keys table from 0023 points at notif_channels too, and
-- survives for the same reason.

CREATE TABLE notif_channels_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL,
    type        TEXT    NOT NULL
                        CHECK (type IN ('webhook', 'discord', 'slack', 'telegram', 'email',
                                        'ntfy', 'gotify', 'sms')),
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
