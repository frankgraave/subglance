-- 0031_status_page_branding.sql — a status page's own look: language,
-- accent colour, footer credit and logo (docs/design/status-page.md §2.1).
--
-- The settings sit on the page row. The logo is a table of its own, so the
-- many reads of a page's settings never pull up to 256 KB of image with them,
-- and a page without a logo has no row rather than a NULL blob.

-- The fixed texts of the public page: English or Dutch.
ALTER TABLE status_pages ADD COLUMN language TEXT NOT NULL DEFAULT 'en'
    CHECK (language IN ('en', 'nl'));
-- '' for none, otherwise #rrggbb in lowercase. The contrast rule is checked
-- on save, in Go, where the refusal can state the measured ratio.
ALTER TABLE status_pages ADD COLUMN accent TEXT NOT NULL DEFAULT ''
    CHECK (accent = '' OR (length(accent) = 7 AND accent GLOB '#[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]'));
-- The "Monitored with SubGlance" credit in the footer: shown unless the
-- operator hides it. The footer's time-zone line stays either way, since the
-- page's times mean nothing without it.
ALTER TABLE status_pages ADD COLUMN hide_credit INTEGER NOT NULL DEFAULT 0
    CHECK (hide_credit IN (0, 1));

-- One logo per page. content_type is decided from the bytes on upload, never
-- from a file name or a request header, and only these three are stored: an
-- SVG can carry script, and every other type is something a browser might
-- render as a document.
--
-- file_key names the image in its public URL. It is random and changes on
-- every upload, so a cache may keep the file for as long as it likes: a new
-- logo is a new address.
CREATE TABLE status_page_logos (
    page_id      INTEGER PRIMARY KEY REFERENCES status_pages (id) ON DELETE CASCADE,
    content_type TEXT    NOT NULL CHECK (content_type IN ('image/png', 'image/jpeg', 'image/webp')),
    width        INTEGER NOT NULL CHECK (width > 0),
    height       INTEGER NOT NULL CHECK (height > 0),
    file_key     TEXT    NOT NULL UNIQUE,
    data         BLOB    NOT NULL CHECK (length(data) BETWEEN 1 AND 262144),
    updated_at   INTEGER NOT NULL
) STRICT;
