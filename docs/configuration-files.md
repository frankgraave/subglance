# Configuration files

SubGlance can write its configuration to a YAML file and read it back:
monitors, notification channels, routing rules and maintenance windows. Use it
to move a setup to another instance, to keep it in a repository where changes
show up as diffs, or to create forty monitors from a file instead of forty
forms.

It is not a backup. History, users and API tokens are not in the file, and
neither are credentials. For a full copy of an instance, see
[Backup and restore](operations.md#backup-and-restore).

- [Exporting](#exporting)
- [Importing](#importing)
- [What never leaves the instance](#what-never-leaves-the-instance)
- [Keys](#keys)
- [The format](#the-format)

## Exporting

On **Settings → Import & export**, *Download configuration* saves the file as
`subglance-config.yaml`. From a script:

```sh
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/config/export > subglance.yaml
```

Export needs an editor or an administrator. The file lists every monitor,
channel, routing rule and maintenance window. One-off maintenance windows that
have already ended are left out: they are history, not configuration.

## Importing

On **Settings → Import & export**, choosing a file runs the dry run below and
shows its report: what each object would become, which fields an update
changes, and which objects need a value filled in after the import. A refused
file shows the error at its place in the file. Nothing is written until you
confirm the import.

From a script, look first. A dry run reports what the import would do and writes nothing:

```sh
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/yaml' \
  --data-binary @subglance.yaml \
  'http://localhost:8080/api/v1/config/import?dry_run=true'
```

The report lists every object in the file with its `action` (`create`,
`update` or `unchanged`), the fields an update `changes`, and any
`needs_secrets` (see below). Run it again without `dry_run` to apply it.

Import follows three rules.

- **It never deletes.** A monitor on the instance that the file does not
  mention is left alone. There is no mode in which the file replaces the
  instance.
- **It is all or nothing on validation.** Every object is checked against the
  same rules the API applies before the first write. One bad field refuses the
  whole file, and the error says where: `monitors[3].interval_s: interval_s
  must be between 20 and 86400`.
- **Twice is the same as once.** Monitors and channels are matched by key,
  routing rules by their tag pair, and maintenance windows by all of their
  fields, so importing a file a second time changes nothing.

A field the file leaves out keeps its current value on an existing object, and
gets the same default the API gives a new one. A hand-written file can
therefore rename one monitor without restating everything else about it:

```yaml
version: 1
monitors:
  - key: shop
    name: Webshop
```

Lists work the same way: an omitted `channels` or `tags` leaves the current
ones alone, and an empty one (`channels: []`, `tags: {}`) clears them.

A push monitor created by an import is issued a new push URL. It is shown once,
as `push_url` in the import report, exactly like the response that creates a
push monitor through the API. Update the job that pings it.

## What never leaves the instance

A value that proves the right to send or read something is written as
`<fill in after import>`:

- every channel setting except the ones that say where a message goes (`to`,
  `from`, `chat_id`, `channel`, `username`, `host`, `port`), which is the same
  rule the channel API uses when it masks a read;
- the value of every request header an HTTP monitor sends (the header names
  stay, so the file still says which headers are sent);
- an HTTP monitor's request body.

The push URL of a push monitor is not written at all.

On import, a placeholder keeps what the instance already holds for that field,
so exporting and re-importing on the same instance changes nothing. On an
instance that has nothing to keep, the object is created without the value and
**switched off**, and the report lists the field under `needs_secrets`. A
channel without its token, or a check without its authorization header, would
fail every attempt and look like an outage, so it waits for you instead. Fill
the value in, either in the file before importing or in the interface
afterwards, and switch the object on.

## Keys

Every monitor and channel in a file has a `key`: lowercase letters, digits,
dots, dashes and underscores, at most 64 characters. It is how an import finds
the object it created last time.

An object that has never been exported has no key. The first export derives
one from its name (`API (prod)` becomes `api-prod`, with `-2`, `-3` added when
two names collide) and stores it. From then on the key belongs to the object:
renaming the monitor in the interface does not change it, so the next import
still updates the same monitor instead of creating a second one. Deleting the
object deletes its key.

A monitor and a channel may share a key; they are looked up separately.

## The format

```yaml
version: 1
channels:
  - key: ops-slack
    name: Ops Slack
    type: slack
    enabled: true
    config:
      url: <fill in after import>
  - key: on-call
    name: On call
    type: telegram
    enabled: true
    config:
      bot_token: <fill in after import>
      chat_id: "-100200"
    quiet_hours:
      start: "23:00"
      end: "07:00"
      timezone: Europe/Amsterdam
      during: hold
monitors:
  - key: api-prod
    name: API (prod)
    type: http
    target: https://api.example.com/health
    enabled: true
    interval_s: 30
    timeout_s: 10
    retries: 3
    recovery_threshold: 2
    repeat_after_s: 900
    method: GET
    expected_status: 200-299
    keyword: ""
    keyword_mode: absent_ok
    follow_redirects: true
    capture_response: true
    headers:
      Authorization: <fill in after import>
    json_assertion:
      path: checks.db.status
      operator: equals
      expected: up
    ssl_warn_days: 14
    min_tls_version: "1.2"
    tags:
      env: prod
    channels: [ops-slack, on-call]
  - key: nightly-backup
    name: Nightly backup
    type: push
    enabled: true
    push_interval_s: 86400
    push_grace_s: 600
    tags: {}
    channels: []
routing_rules:
  - tag_key: env
    tag_value: prod
    channels: [on-call]
    exclude: [nightly-backup]
maintenance:
  - name: Patch night
    tag_key: env
    tag_value: prod
    timezone: Europe/Amsterdam
    weekdays: [2]
    local_time: "02:00"
    duration_minutes: 60
```

Field names and allowed values are the ones the API uses; see
[`openapi.yaml`](openapi.yaml) and [Using SubGlance](using-subglance.md).
A few points specific to the file:

- `version` is required. This release reads and writes version 1 and refuses
  any other, rather than guessing what a newer file means. A change to the
  format raises the number, and a newer SubGlance keeps reading older files.
- Unknown fields are an error, not ignored. `interval: 30` instead of
  `interval_s: 30` would otherwise import cleanly and leave the monitor on its
  old schedule.
- A file holds one YAML document. A second document after `---` is refused
  instead of being silently dropped.
- `json_assertion: null` removes a monitor's assertion; leaving the field out
  keeps it. `expected` is a plain YAML value, and its type matters: `1` is the
  number and `"1"` the string, exactly as in the API.
- `default: true` on a channel makes it the instance default. An import never
  clears the default, so a file that does not mention one leaves it as it is.
- A reference (`channels`, `exclude`, a maintenance window's `monitor`) may name
  an object that is in the file or one that already has that key on the
  instance.
- A maintenance window is identified by all of its fields. Editing a window in
  the file and importing it adds the edited window next to the old one; delete
  the old one in the interface.
- The file is limited to 4 MiB.
