-- 0023_config_keys.sql — stable keys for configuration files.
--
-- A configuration file (internal/configfile) names monitors and channels by a
-- key rather than by id, because ids differ between instances and a file is
-- meant to move between them. Importing the same file twice must find the
-- objects it created the first time, and renaming a monitor in the interface
-- must not make the next import create a second one. So the key is stored,
-- rather than derived from the name each time.
--
-- Separate tables instead of a column on monitors and notif_channels: both
-- tables have been rebuilt by migrations before (SQLite cannot alter a CHECK
-- constraint in place), and a rebuild that copies an explicit column list
-- would drop a column it does not know about. A child table survives that
-- rebuild, and ON DELETE CASCADE removes the key with its object.
--
-- Most objects never get a key: one is assigned the first time the object is
-- exported or imported.
CREATE TABLE monitor_config_keys (
    monitor_id INTEGER PRIMARY KEY REFERENCES monitors (id) ON DELETE CASCADE,
    key        TEXT    NOT NULL UNIQUE
) STRICT;

CREATE TABLE channel_config_keys (
    channel_id INTEGER PRIMARY KEY REFERENCES notif_channels (id) ON DELETE CASCADE,
    key        TEXT    NOT NULL UNIQUE
) STRICT;
