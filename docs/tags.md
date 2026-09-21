# Managing tags across monitors

In **Monitors**, select monitors and choose **Manage tags** on the Configured
monitors card. **Select all visible** adds only the monitors shown by current
filters. Earlier selections remain selected when hidden; the count says how
many are hidden. Clear the selection when you want to start again.

**Apply** adds or replaces one key/value on selected monitors. **Remove** deletes
only that exact key/value pair. Preview the counts, then confirm the atomic
change. Other tags and monitor settings are preserved. Invalid input or a
storage failure applies nothing, not an unreported partial result.

## Renaming throughout the instance

Choose **Rename key across instance** or **Rename value across instance**. These
affect every matching monitor, not just selected or visible monitors.

A key rename keeps an existing destination key's value and removes the old key.
The preview explains that merge policy and reports collisions before you
confirm. A value rename matches both the key and the old value, leaving other
keys and values alone. Dashboard facets and grouping refresh without reloading
the page. Tags have no registry: a key exists while a monitor carries it.

## API and concurrent changes

Preview with `POST /api/v1/monitors/tags/preview`, then send the **same body** to
`POST /api/v1/monitors/tags` with the preview ETag in `If-Match`. Both routes need
editor or administrator access. The API refuses an unconditional commit.

Actions are `apply`, `remove`, `rename_key` and `rename_value`; exact schemas are
in [OpenAPI](openapi.yaml). Bodies are limited to 256 KiB; explicit selections
to 10000 unique monitor IDs. Global renames have no matching-monitor cap.

A 412 means relevant tags or matching monitors changed after the preview. Fetch
a new preview and review it before confirming, rather than blindly retrying.
Every changed monitor receives a new revision; an older monitor-edit ETag
cannot overwrite the tag operation. Unchanged monitors keep their revision.
