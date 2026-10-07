import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { StateChip } from "../components/Chip";
import { Checkbox } from "../components/Choice";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { Drawer } from "../components/Drawer";
import { IconGlobe, IconTrash } from "../components/icons";
import { ApiError } from "../api/http";
import { registerLeaveGuard } from "../shell/leaveGuard";
import { browserTimezone, knownTimezones } from "../notifications/quietHours";
import { fetchInventory, inventoryQueryKey } from "../monitors/inventoryApi";
import type { InventoryMonitor } from "../monitors/inventory";
import {
  createStatusPage, deleteStatusPage, fetchStatusPages, logoPreviewURL, MAX_LOGO_BYTES, removeStatusPageLogo,
  setStatusPageEntries, slugFrom, statusPagesKey, updateStatusPage, uploadStatusPageLogo,
  type EntryInput, type Language, type Selection, type StatusPage, type StatusPageSettings,
} from "./api";
import { Select } from "../components/Select";
import { FieldError } from "../components/FieldError";
import { FileInput } from "../components/FileInput";
import "./statuspages.css";

type Rejection = { field?: string; message: string };
const rejectionOf = (error: unknown): Rejection => error instanceof ApiError
  ? { field: error.field ?? undefined, message: error.message }
  : { message: error instanceof Error ? error.message : "Could not reach SubGlance. Check your connection and try again." };

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;
/** The address a visitor opens, relative to wherever this instance is served. */
const address = (slug: string) => `/status/${slug}`;

/**
 * Keeps a drawer's unsaved draft from being dropped by Escape, the scrim or a
 * navigation, through the same guard the monitor forms use.
 */
function useDraftGuard(what: string, dirty: boolean) {
  const form = useRef<HTMLFormElement>(null);
  const latest = useRef(dirty);
  useEffect(() => { latest.current = dirty; });
  useEffect(() => registerLeaveGuard({
    element: () => form.current, dirty: () => latest.current, discard: () => { latest.current = false; }, what,
  }), [what]);
  return form;
}

/** A labelled input whose server refusal is drawn under it and named by it. */
function Field({ id, label, help, error, children }: {
  id: string; label: string; help?: ReactNode; error?: string; children: ReactNode;
}) {
  return <div className="field">
    <label className="field-label" htmlFor={id}>{label}</label>
    {children}
    {help && <p className="panel-note" id={`${id}-help`}>{help}</p>}
    {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
  </div>;
}

const SETTINGS = ["slug", "title", "description", "timezone", "selection", "tag_key", "tag_value", "indexable", "enabled",
  "language", "accent", "hide_credit"] as const;
const settingsOf = (page: StatusPage) => Object.fromEntries(SETTINGS.map((key) => [key, page[key]])) as StatusPageSettings;

/**
 * A page's settings: create one, or change one.
 *
 * Every setting is sent on every save, because the server replaces them all
 * and reads an omitted boolean as false. A form that sent only what changed
 * would take a published page offline the first time someone fixed a typo.
 */
function SettingsForm({ page, onSaved, onLogo, onCancel }: {
  page: StatusPage | null; onSaved: (page: StatusPage) => void; onLogo: (page: StatusPage) => void; onCancel: () => void;
}) {
  const id = useId();
  // Only the settings, never the whole page: the server refuses unknown
  // fields, and `id` or `entries` in the body would be one.
  const initial: StatusPageSettings = page ? settingsOf(page) : {
    slug: "", title: "", description: "", timezone: browserTimezone(), selection: "monitors",
    tag_key: "", tag_value: "", indexable: false, enabled: false, language: "en", accent: "", hide_credit: false,
  };
  const [draft, setDraft] = useState<StatusPageSettings>(initial);
  // On a new page the address follows the title until it is typed in by hand.
  const [slugTouched, setSlugTouched] = useState(page !== null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<Rejection | null>(null);
  const dirty = SETTINGS.some((key) => draft[key] !== initial[key]);
  const form = useDraftGuard("status page", dirty && !saving);
  const zones = knownTimezones();
  const set = <K extends keyof StatusPageSettings>(key: K, value: StatusPageSettings[K]) => {
    setDraft((old) => ({ ...old, [key]: value, ...(key === "title" && !slugTouched ? { slug: slugFrom(String(value)) } : {}) }));
    setError(null);
  };
  const fieldError = (field: string) => error?.field === field ? error.message : undefined;
  const described = (field: string, help = false) => [help ? `${id}-${field}-help` : "", fieldError(field) ? `${id}-${field}-error` : ""]
    .filter(Boolean).join(" ") || undefined;
  const input = (field: keyof StatusPageSettings, help = false) => ({
    id: `${id}-${field}`, className: "input input--inset", "aria-describedby": described(field, help),
    "aria-invalid": fieldError(field) ? true : undefined,
  });
  const ready = draft.title.trim() !== "" && draft.slug.trim() !== "" &&
    (draft.selection === "monitors" || (draft.tag_key.trim() !== "" && draft.tag_value.trim() !== ""));
  const renamed = page !== null && page.enabled && draft.slug.trim().toLowerCase() !== page.slug;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready || saving) return;
    setSaving(true);
    setError(null);
    const body = { ...draft, ...(draft.selection === "monitors" ? { tag_key: "", tag_value: "" } : {}) };
    try {
      onSaved(page ? await updateStatusPage(page.slug, body) : await createStatusPage(body));
    } catch (err) {
      setError(rejectionOf(err));
      setSaving(false);
    }
  }

  // A tag error has no input of its own while the page selects monitors,
  // so it falls back to the form's general region.
  const placed = ["slug", "title", "description", "timezone", "selection", "tag_key", "tag_value", "language", "accent"];
  const general = error && !(error.field && placed.includes(error.field) && (draft.selection === "tag" || !error.field.startsWith("tag_")));

  return <form ref={form} className="stack" aria-label={page ? `Settings for ${page.title}` : "New status page"} onSubmit={submit}>
    <Field id={`${id}-title`} label="Title" help="The heading visitors see." error={fieldError("title")}>
      <input {...input("title", true)} value={draft.title} maxLength={120} autoComplete="off"
        onChange={(event) => set("title", event.target.value)} />
    </Field>
    <Field id={`${id}-slug`} label="Address"
      help={<>Visitors open <code>{address(draft.slug.trim().toLowerCase() || "…")}</code>. Lowercase letters, digits and dashes.</>}
      error={fieldError("slug")}>
      <input {...input("slug", true)} value={draft.slug} maxLength={63} autoComplete="off" spellCheck={false}
        onChange={(event) => { setSlugTouched(true); set("slug", event.target.value); }} />
    </Field>
    {renamed && page && <p className="panel-note" role="status">Links already shared to {address(page.slug)} stop working when you save.</p>}
    <Field id={`${id}-description`} label="Description (optional)" help="Plain text under the title." error={fieldError("description")}>
      <textarea {...input("description", true)} rows={2} value={draft.description} maxLength={500}
        onChange={(event) => set("description", event.target.value)} />
    </Field>
    <Field id={`${id}-timezone`} label="Time zone" help="Times on the page are shown in this zone, and the page names it."
      error={fieldError("timezone")}>
      <input {...input("timezone", true)} value={draft.timezone} list={`${id}-zones`} autoComplete="off" spellCheck={false}
        onChange={(event) => set("timezone", event.target.value)} />
      <datalist id={`${id}-zones`}>{zones.map((zone) => <option key={zone} value={zone}>{zone}</option>)}</datalist>
    </Field>
    {/* A select, not the segmented control: its unselected segment is
        below the AA contrast floor (a waived, shared debt), and a form in a
        drawer should not add a fourth measured instance of it. */}
    <Field id={`${id}-selection`} label="Shows" error={fieldError("selection")}
      help={draft.selection === "monitors" ? "Exactly the monitors you add to the page." : "Monitors carrying this tag, once each has a public name."}>
      <Select {...input("selection", true)} value={draft.selection} onChange={(event) => set("selection", event.target.value as Selection)}>
        <option value="monitors">Chosen monitors</option>
        <option value="tag">Monitors with a tag</option>
      </Select>
    </Field>
    {draft.selection === "tag" && <div className="control-row">
      <Field id={`${id}-tag_key`} label="Tag key" error={fieldError("tag_key")}>
        <input {...input("tag_key")} value={draft.tag_key} placeholder="customer" autoComplete="off" spellCheck={false}
          onChange={(event) => set("tag_key", event.target.value)} />
      </Field>
      <Field id={`${id}-tag_value`} label="Tag value" error={fieldError("tag_value")}>
        <input {...input("tag_value")} value={draft.tag_value} placeholder="acme" autoComplete="off" spellCheck={false}
          onChange={(event) => set("tag_value", event.target.value)} />
      </Field>
    </div>}
    {/* The page's own look (docs/design/status-page.md §2.1). */}
    <Field id={`${id}-language`} label="Language" help="The language of the page's fixed texts: status words, dates, headings."
      error={fieldError("language")}>
      <Select {...input("language", true)} value={draft.language} onChange={(event) => set("language", event.target.value as Language)}>
        <option value="en">English</option>
        <option value="nl" lang="nl">Nederlands</option>
      </Select>
    </Field>
    {/* A text field, not a colour picker: an operator has the brand's code to
        paste, and a picker cannot be empty, which is how "no accent" is said. */}
    <Field id={`${id}-accent`} label="Accent colour (optional)" error={fieldError("accent")}
      help="The title's colour, written as #rrggbb. It has to read on both the light and the dark page, since the page follows each visitor's theme.">
      <input {...input("accent", true)} value={draft.accent} maxLength={7} autoComplete="off" spellCheck={false}
        onChange={(event) => set("accent", event.target.value)} />
    </Field>
    {page ? <LogoField page={page} onChanged={onLogo} />
      : <p className="panel-note">A logo can be added once the page is created.</p>}
    <Checkbox checked={!draft.hide_credit} onChange={(event) => set("hide_credit", !event.target.checked)}>
      Show &ldquo;Monitored with SubGlance&rdquo; in the footer
    </Checkbox>
    <Checkbox checked={draft.enabled} aria-describedby={`${id}-enabled-help`}
      onChange={(event) => set("enabled", event.target.checked)}>
      Published
    </Checkbox>
    <p className="panel-note" id={`${id}-enabled-help`}>
      {draft.enabled ? "Anyone with the address can open the page." : "Off: the address answers as if there were no page."}
    </p>
    <Checkbox checked={draft.indexable} onChange={(event) => set("indexable", event.target.checked)}>
      Let search engines list it
    </Checkbox>
    {general && error && <FieldError>{error.message}</FieldError>}
    <div className="button-row">
      <button className="button-solid" type="submit" disabled={!ready || saving}>
        {saving ? "Saving…" : page ? "Save settings" : "Create page"}
      </button>
      <button className="button" type="button" onClick={onCancel}>Cancel</button>
    </div>
  </form>;
}

/**
 * The page's logo: shown, replaced or removed at once rather than on "Save
 * settings", because a file is not a draft value that can be typed back.
 *
 * The file is checked for size here only to save a pointless upload; the
 * server decides what the file is, from its bytes.
 */
function LogoField({ page, onChanged }: { page: StatusPage; onChanged: (page: StatusPage) => void }) {
  const id = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [inputVersion, setInputVersion] = useState(0);
  const logo = page.logo;

  async function run(action: () => Promise<StatusPage>) {
    setBusy(true);
    setError(null);
    try {
      onChanged(await action());
    } catch (err) {
      setError(rejectionOf(err).message);
    } finally {
      setBusy(false);
      setInputVersion((v) => v + 1);
    }
  }

  function choose(file: File | undefined) {
    if (!file) return;
    if (file.size > MAX_LOGO_BYTES) {
      setError(`The logo is larger than ${MAX_LOGO_BYTES / 1024} KB.`);
      setInputVersion((v) => v + 1);
      return;
    }
    void run(() => uploadStatusPageLogo(page.slug, file));
  }

  return <div className="field">
    <label className="field-label" htmlFor={`${id}-file`}>{logo ? "Replace logo" : "Logo (optional)"}</label>
    {logo && <div className="control-row">
      <img className="sp-logo-preview" src={logoPreviewURL(page.slug, logo)} width={logo.width} height={logo.height}
        alt={`Current logo of ${page.title}`} />
      <button type="button" className="button" disabled={busy} onClick={() => void run(() => removeStatusPageLogo(page.slug))}>
        Remove logo
      </button>
    </div>}
    <FileInput key={inputVersion} id={`${id}-file`} accept="image/png,image/jpeg,image/webp" disabled={busy}
      aria-describedby={[`${id}-help`, error ? `${id}-error` : ""].filter(Boolean).join(" ")}
      aria-invalid={error ? true : undefined} onChange={(event) => choose(event.target.files?.[0])} />
    <p className="panel-note" id={`${id}-help`}>
      PNG, JPEG or WebP, up to {MAX_LOGO_BYTES / 1024} KB. Shown above the title; use one that reads on a light and a
      dark background. It changes as soon as it is chosen.
    </p>
    <div role="status" aria-live="polite" className="result-region">{busy && <p className="panel-note">Uploading…</p>}</div>
    {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
  </div>;
}

type Draft = { monitor_id: number; display_name: string };

/**
 * The monitors a page shows, in order, each under its public name.
 *
 * A monitor added here starts with an empty public name, never its own:
 * pre-filling it would make publishing an internal name one click away, and
 * the design's one hard rule is that internal names do not leak.
 */
function ServicesForm({ page, monitors, onSaved, onCancel }: {
  page: StatusPage; monitors: readonly InventoryMonitor[]; onSaved: (page: StatusPage) => void; onCancel: () => void;
}) {
  const id = useId();
  const initial = page.entries.map(({ monitor_id, display_name }) => ({ monitor_id, display_name }));
  const [draft, setDraft] = useState<Draft[]>(initial);
  const [adding, setAdding] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [blank, setBlank] = useState<number | null>(null);
  const inputs = useRef(new Map<number, HTMLInputElement>());
  const focusNext = useRef<number | null>(null);
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial);
  const form = useDraftGuard("status page", dirty && !saving);
  useEffect(() => {
    if (focusNext.current === null) return;
    inputs.current.get(focusNext.current)?.focus();
    focusNext.current = null;
  });

  const byId = new Map(monitors.map((m) => [Number(m.id), m]));
  const nameOf = (monitorId: number) => byId.get(monitorId)?.name ?? `Monitor ${monitorId}`;
  const tagged = (monitorId: number) => page.selection === "monitors" || byId.get(monitorId)?.tags[page.tag_key] === page.tag_value;
  const chosen = new Set(draft.map((entry) => entry.monitor_id));
  // A tag page can only show monitors with the tag, so only those are offered.
  const candidates = monitors.filter((m) => !chosen.has(Number(m.id)) && tagged(Number(m.id)));
  const unnamed = page.unnamed_monitor_ids.filter((monitorId) => !chosen.has(monitorId));

  const change = (next: Draft[]) => { setDraft(next); setError(null); setBlank(null); };
  const move = (index: number, by: number) => {
    const next = [...draft];
    [next[index], next[index + by]] = [next[index + by], next[index]];
    change(next);
  };
  const add = (monitorId: number) => {
    focusNext.current = monitorId;
    change([...draft, { monitor_id: monitorId, display_name: "" }]);
    setAdding("");
  };

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (saving) return;
    const entries: EntryInput[] = draft.map((entry) => ({ ...entry, display_name: entry.display_name.trim() }));
    const missing = entries.find((entry) => entry.display_name === "");
    if (missing) {
      setBlank(missing.monitor_id);
      setError(`Give ${nameOf(missing.monitor_id)} a public name, or remove it from the page.`);
      inputs.current.get(missing.monitor_id)?.focus();
      return;
    }
    setSaving(true);
    setError(null);
    try {
      onSaved(await setStatusPageEntries(page.slug, entries));
    } catch (err) {
      setError(rejectionOf(err).message);
      setSaving(false);
    }
  }

  return <form ref={form} className="stack" aria-label={`Services on ${page.title}`} onSubmit={submit}>
    <p className="panel-note">
      Visitors see only the public name, the state and the history. The monitor&apos;s own name, address and errors never
      appear on the page.
      {page.selection === "tag" && <> This page shows monitors tagged <code>{page.tag_key}:{page.tag_value}</code>.</>}
    </p>
    {unnamed.length > 0 && <p className="panel-note" role="status">
      Not shown until named: {unnamed.map(nameOf).join(", ")}.
    </p>}
    {draft.length === 0 ? <p className="panel-note">No services on this page yet.</p>
      : <ol className="stack" aria-label="Services, in page order">
        {draft.map((entry, index) => {
          const monitor = nameOf(entry.monitor_id);
          const invalid = blank === entry.monitor_id;
          return <li key={entry.monitor_id} className="control-row">
            <div className="field control-row-main">
              <label className="field-label" htmlFor={`${id}-${entry.monitor_id}`}>Public name for {monitor}</label>
              <input className="input input--inset" id={`${id}-${entry.monitor_id}`} value={entry.display_name} maxLength={80}
                autoComplete="off" ref={(node) => { if (node) inputs.current.set(entry.monitor_id, node); else inputs.current.delete(entry.monitor_id); }}
                aria-invalid={invalid ? true : undefined} aria-describedby={invalid ? `${id}-error` : undefined}
                onChange={(event) => change(draft.map((item) => item.monitor_id === entry.monitor_id
                  ? { ...item, display_name: event.target.value } : item))} />
              {!tagged(entry.monitor_id) && <p className="panel-note">Not shown: this monitor no longer has the tag.</p>}
            </div>
            <span className="button-row">
              <button type="button" className="button" disabled={index === 0} aria-label={`Move ${monitor} up`}
                onClick={() => move(index, -1)}>Up</button>
              <button type="button" className="button" disabled={index === draft.length - 1}
                aria-label={`Move ${monitor} down`} onClick={() => move(index, 1)}>Down</button>
              <button type="button" className="button" aria-label={`Remove ${monitor} from the page`}
                onClick={() => change(draft.filter((item) => item.monitor_id !== entry.monitor_id))}>Remove</button>
            </span>
          </li>;
        })}
      </ol>}
    {candidates.length > 0 && <div className="control-row">
      <div className="field control-row-main">
        <label className="field-label" htmlFor={`${id}-add`}>Add a monitor</label>
        <Select className="input input--inset" id={`${id}-add`} value={adding} onChange={(event) => setAdding(event.target.value)}>
          <option value="">Choose a monitor</option>
          {candidates.map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}
        </Select>
      </div>
      <button type="button" className="button" disabled={adding === ""} onClick={() => add(Number(adding))}>Add</button>
    </div>}
    {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
    <div className="button-row">
      <button className="button-solid" type="submit" disabled={saving || !dirty}>{saving ? "Saving…" : "Save services"}</button>
      <button className="button" type="button" onClick={onCancel}>Cancel</button>
    </div>
  </form>;
}

function PageRow({ page, onEdit, onServices, onDeleted }: {
  page: StatusPage; onEdit: () => void; onServices: () => void; onDeleted: (message: string) => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const facts = [
    address(page.slug),
    plural(page.entries.length, "service"),
    ...(page.unnamed_monitor_ids.length > 0 ? [`${page.unnamed_monitor_ids.length} not shown until named`] : []),
    page.indexable ? "search engines may list it" : "hidden from search engines",
  ];

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await deleteStatusPage(page.slug);
      onDeleted(`${page.title} was deleted.`);
    } catch (err) {
      setError(rejectionOf(err).message);
      setBusy(false);
    }
  }

  return <li className="control-row">
    <div className="control-row-main">
      <p><strong>{page.title}</strong></p>
      <p className="panel-note">{facts.join(" · ")}</p>
      {error && <FieldError>{error}</FieldError>}
    </div>
    <StateChip>{page.enabled ? "published" : "off"}</StateChip>
    <span className="button-row">
      {/* Only a published page has anything to open: one that is off answers
          404, exactly like an address that never existed. A plain link, so
          the page loads as the visitor gets it, without the dashboard. */}
      {page.enabled && <a className="button" href={address(page.slug)} aria-label={`Open ${page.title}`}>Open</a>}
      <button type="button" className="button" disabled={busy} aria-label={`Settings for ${page.title}`} onClick={onEdit}>Settings</button>
      <button type="button" className="button" disabled={busy} aria-label={`Services on ${page.title}`} onClick={onServices}>Services</button>
    </span>
    <button type="button" className="icon-button button--danger" disabled={busy}
      aria-label={`Delete ${page.title}`} title={`Delete ${page.title}`} onClick={() => setConfirming(true)}>
      <IconTrash />
    </button>
    {/* The address is retyped, not the title: it is unique, and it is what
        visitors have bookmarked. */}
    {confirming && <ConfirmDelete open onClose={() => setConfirming(false)} kind="status page" name={page.slug}
      consequence={`${address(page.slug)} stops answering, and the page's settings and services are deleted. The monitors and their history stay. This cannot be undone.`}
      onConfirm={() => { setConfirming(false); void remove(); }} />}
  </li>;
}

type Editing = { kind: "settings"; pageId: number | null } | { kind: "services"; pageId: number };

/**
 * Public status pages: create one, choose what it shows, publish it.
 *
 * Administrators only, like the API behind it: a page decides what the
 * instance tells people without an account, which is an instance-wide call.
 * Loaded on demand (see Settings.tsx), so the entry bundle does not carry an
 * editor that most sessions never open.
 */
export function StatusPagesCard() {
  const client = useQueryClient();
  const query = useQuery({ queryKey: statusPagesKey, queryFn: ({ signal }) => fetchStatusPages(signal) });
  const monitors = useQuery({ queryKey: inventoryQueryKey, queryFn: ({ signal }) => fetchInventory(signal) });
  const [editing, setEditing] = useState<Editing | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const pages = query.data;
  const current = editing?.pageId == null ? null : pages?.find((page) => page.id === editing.pageId) ?? null;
  const store = (page: StatusPage) => client.setQueryData<StatusPage[]>(statusPagesKey,
    (old) => old?.some((item) => item.id === page.id) ? old.map((item) => item.id === page.id ? page : item) : [...(old ?? []), page]);
  const close = () => setEditing(null);
  // An existing page that a refetch no longer lists (deleted elsewhere) must not
  // fall back to the new-page form: saving that would create a duplicate.
  const settingsOpen = editing?.kind === "settings" && (editing.pageId === null || current !== null);

  return <><Card title={pages ? `Status pages (${pages.length})` : "Status pages"} icon={<IconGlobe />}
    action={<button type="button" className="button" onClick={() => { setEditing({ kind: "settings", pageId: null }); setMessage(null); }}>New page</button>}>
    <Panel spacing="form">
      <p className="panel-note">
        A public page for people without an account: chosen monitors under public names, their state and 90 days of
        history. A new page is off until you publish it.
      </p>
      <div role="status" aria-live="polite" className="result-region">{message && <p className="result">{message}</p>}</div>
      {!pages ? <p>{query.isError ? "Status pages unavailable." : "Loading status pages…"}</p>
        : pages.length === 0 ? <p className="panel-note">No status pages yet.</p>
        : <ul className="stack" aria-label="Status pages">
          {pages.map((page) => <PageRow key={page.id} page={page}
            onEdit={() => { setEditing({ kind: "settings", pageId: page.id }); setMessage(null); }}
            onServices={() => { setEditing({ kind: "services", pageId: page.id }); setMessage(null); }}
            onDeleted={(text) => { setMessage(text); void client.invalidateQueries({ queryKey: statusPagesKey }); }} />)}
        </ul>}
    </Panel>
  </Card>
    {/* Beside the card rather than inside it, so no card rule can ever
        become the drawer's containing block. */}
    <Drawer open={settingsOpen} onClose={close} title={current ? `Settings for ${current.title}` : "New status page"}>
      <Panel>
        {settingsOpen && <SettingsForm key={editing.pageId ?? "new"} page={current} onCancel={close} onLogo={store}
          onSaved={(page) => {
            store(page);
            if (editing.pageId === null) {
              // A page with nothing on it is not finished; the next step is choosing what it shows.
              setMessage(`${page.title} was created. Add the services it shows, then publish it.`);
              setEditing({ kind: "services", pageId: page.id });
            } else {
              setMessage(`Settings for ${page.title} were saved.`);
              close();
            }
          }} />}
      </Panel>
    </Drawer>
    <Drawer open={editing?.kind === "services" && current !== null} onClose={close} title={current ? `Services on ${current.title}` : "Services"}>
      <Panel>
        {editing?.kind === "services" && current && (monitors.data
          ? <ServicesForm key={current.id} page={current} monitors={monitors.data} onCancel={close}
            onSaved={(page) => { store(page); setMessage(`Services on ${page.title} were saved.`); close(); }} />
          : <p>{monitors.isError ? "Monitors unavailable, so the services cannot be changed right now." : "Loading monitors…"}</p>)}
      </Panel>
    </Drawer>
  </>;
}
