import { useEffect, useId, useRef, useState, type ChangeEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { IconTransfer } from "../components/icons";
import { StateChip } from "../components/Chip";
import { ApiError } from "../api/http";
import { PushUrlReveal } from "../monitors/PushUrlReveal";
import { registerLeaveGuard } from "../shell/leaveGuard";
import { GROUPS, MAX_FILE_BYTES, changing, downloadConfig, importConfig, pushUrls, type ImportItem, type ImportReport } from "./api";
import { FileInput } from "../components/FileInput";

const docs = "https://github.com/frankgraave/subglance/blob/develop/docs/configuration-files.md";

/** A refusal, and where in the file it points when it points somewhere. */
type Problem = { message: string; path?: string };

type ImportState =
  | { phase: "idle" }
  | { phase: "checking" | "applying"; file: File; report?: ImportReport }
  | { phase: "checked"; file: File; text: string; report: ImportReport }
  | { phase: "refused"; file: File; problem: Problem; applied: boolean }
  | { phase: "applied"; report: ImportReport; reveal: { url: string; name: string }[] };

function explain(error: unknown): Problem {
  if (error instanceof ApiError) return { message: error.message, ...(error.field ? { path: error.field } : {}) };
  if (error instanceof Error && !(error instanceof TypeError)) return { message: error.message };
  return { message: "Could not reach SubGlance. Check your connection and try again." };
}

/** "3 to create, 1 to update, 12 unchanged", or the past tense after an apply. */
function summary(report: ImportReport): string {
  const { create, update, unchanged } = report.summary;
  const dry = report.dry_run;
  const parts = [
    create > 0 && `${create} ${dry ? "to create" : "created"}`,
    update > 0 && `${update} ${dry ? "to update" : "updated"}`,
    unchanged > 0 && `${unchanged} unchanged`,
  ].filter(Boolean);
  if (create + update === 0) {
    return unchanged > 0
      ? "Everything in this file already matches this instance. There is nothing to import."
      : "This file describes no monitors, channels, routing rules or maintenance windows.";
  }
  return `${parts.join(", ")}.`;
}

const words = (list: string[]) => list.map((word, index) => <span key={word}>{index > 0 && ", "}<code>{word}</code></span>);

function Item({ item }: { item: ImportItem }) {
  const title = item.name || item.key;
  return <li className="field">
    <p>
      <span className="field-label">{item.action === "create" ? "Create" : "Update"}</span>{" "}
      <strong>{title}</strong>
      {item.key && item.name && <> <span className="face-mono">{item.key}</span></>}
      {item.needs_secrets && item.needs_secrets.length > 0 && <> <StateChip>switched off</StateChip></>}
    </p>
    {item.changes && item.changes.length > 0 && <p className="panel-note">Changes {words(item.changes)}</p>}
    {item.needs_secrets && item.needs_secrets.length > 0 && <p className="panel-note">
      Saved switched off: this instance has no value for {words(item.needs_secrets)}. Fill {item.needs_secrets.length === 1 ? "it" : "them"} in after the import, then switch it on.
    </p>}
  </li>;
}

/**
 * What an import would do, or did: one line of counts, then every object it
 * creates or changes. Unchanged objects are counted but not listed, because a
 * re-import of a forty-monitor file would otherwise bury its one change.
 */
function Report({ report }: { report: ImportReport }) {
  const id = useId();
  const secrets = report.summary.needs_secrets;
  return <div className="stack" aria-labelledby={id} role="group">
    <p className="field-label" id={id}>{report.dry_run ? "What this file would change" : "What was imported"}</p>
    <p>{summary(report)}</p>
    {secrets > 0 && <p className="warn-note">
      {secrets === 1 ? "One object is" : `${secrets} objects are`} {report.dry_run ? "saved" : "now"} switched off: the file withholds a
      value this instance has nothing to fill in for. They are marked below.
    </p>}
    {GROUPS.map(([group, label]) => {
      const items = changing(report[group]);
      return items.length > 0 && <div key={group} className="field">
        <p className="field-label" id={`${id}-${group}`}>{label}</p>
        <ul className="stack" aria-labelledby={`${id}-${group}`}>
          {items.map((item, index) => <Item key={`${item.key ?? ""}${item.name ?? ""}${index}`} item={item} />)}
        </ul>
      </div>;
    })}
  </div>;
}

function Refusal({ problem, applied }: { problem: Problem; applied: boolean }) {
  // The push reveal's caveat box, not `.field-error`: --down text on a card
  // panel measures under 4.5:1 in both themes, and this sentence is the whole
  // answer, so it is set in full ink behind a rail instead.
  return <div role="alert" className="warn-note field">
    {problem.path && <p>In the file at <code>{problem.path}</code></p>}
    <p>{problem.message}</p>
    <p className="panel-note">
      {applied ? "Importing the same file again completes the import: what was already written is matched by key and left alone."
        : "Nothing was imported. Fix the file and choose it again."}
    </p>
  </div>;
}

function ExportPanel() {
  const [state, setState] = useState<"idle" | "busy" | "done">("idle");
  const [error, setError] = useState<string | null>(null);
  async function run() {
    setState("busy");
    setError(null);
    try {
      await downloadConfig();
      setState("done");
    } catch (err) {
      setError(explain(err).message);
      setState("idle");
    }
  }
  return <Panel label="Export">
    <p className="panel-note">
      Monitors, notification channels, routing rules and maintenance windows as one YAML file. History, users, API tokens and credentials
      are not in it, so it is not a backup. <a href={docs}>Read about configuration files</a>.
    </p>
    <div><button type="button" className="button" disabled={state === "busy"} onClick={() => void run()}>
      {state === "busy" ? "Exporting…" : "Download configuration"}
    </button></div>
    <div role="status" aria-live="polite" className="result-region">
      {state === "done" && <p className="panel-note">Downloaded as subglance-config.yaml.</p>}
    </div>
    {error && <p className="warn-note" role="alert">{error}</p>}
  </Panel>;
}

function ImportPanel() {
  const id = useId();
  const client = useQueryClient();
  const [state, setState] = useState<ImportState>({ phase: "idle" });
  // Remounting the input is the only way to clear a file input's choice.
  const [inputVersion, setInputVersion] = useState(0);
  // Only the latest request may land: a second file chosen while the first is
  // being checked must not be overwritten by the first one's report.
  const latest = useRef(0);
  // Leaving the page while a push URL is on screen loses the only copy of it,
  // so it is guarded like an unsaved form.
  const unsaved = useRef(false);
  useEffect(() => { unsaved.current = state.phase === "applied" && state.reveal.length > 0; });
  useEffect(() => registerLeaveGuard({
    element: () => null, dirty: () => unsaved.current, discard: () => { unsaved.current = false; }, what: "push URL",
  }), []);

  async function check(event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0];
    const request = ++latest.current;
    if (!file) { setState({ phase: "idle" }); return; }
    if (file.size > MAX_FILE_BYTES) {
      setState({ phase: "refused", file, applied: false, problem: { message: "The file is larger than 4 MiB, the most an import accepts." } });
      return;
    }
    setState({ phase: "checking", file });
    try {
      const text = await file.text();
      const report = await importConfig(text, true);
      if (request === latest.current) setState({ phase: "checked", file, text, report });
    } catch (err) {
      if (request === latest.current) setState({ phase: "refused", file, applied: false, problem: explain(err) });
    }
  }

  async function apply(file: File, text: string, report: ImportReport) {
    const request = ++latest.current;
    setState({ phase: "applying", file, report });
    try {
      const done = await importConfig(text, false);
      if (request !== latest.current) return;
      setState({ phase: "applied", report: done, reveal: pushUrls(done) });
      setInputVersion((version) => version + 1);
    } catch (err) {
      // A 400 on the second request means the instance changed between the
      // check and the apply; a 500 may have written part of the file.
      if (request === latest.current) setState({ phase: "refused", file, applied: !(err instanceof ApiError && err.status < 500), problem: explain(err) });
    } finally {
      // Whatever happened, the monitors and channels on other screens may be
      // out of date now.
      void client.invalidateQueries();
    }
  }

  function discard() {
    latest.current += 1;
    setState({ phase: "idle" });
    setInputVersion((version) => version + 1);
  }

  if (state.phase === "applied" && state.reveal.length > 0) {
    const [next, ...rest] = state.reveal;
    // One step per created push monitor: each URL exists only in the response
    // that is on screen now, so none of them may be dismissed as a batch.
    return <Panel label="Import">
      <PushUrlReveal key={next.url} url={next.url} name={next.name} onDone={() => setState({ ...state, reveal: rest })} />
    </Panel>;
  }

  const busy = state.phase === "checking" || state.phase === "applying";
  // The dry run stays on screen while its file is applied.
  const shown = state.phase === "checked" || state.phase === "applied" || state.phase === "applying" ? state.report : undefined;
  return <Panel label="Import">
    <p className="panel-note">
      An import creates and updates what the file describes and never deletes anything. The file is checked first; nothing is written until
      you confirm.
    </p>
    <div className="field">
      <label className="field-label" htmlFor={`${id}-file`}>Configuration file</label>
      <FileInput key={inputVersion} id={`${id}-file`}
        accept=".yaml,.yml,application/yaml,text/yaml" disabled={busy} onChange={(event) => void check(event)} />
    </div>
    <div role="status" aria-live="polite" className="result-region">
      {state.phase === "checking" && <p className="panel-note">Checking {state.file.name}…</p>}
      {state.phase === "applying" && <p className="panel-note">Importing {state.file.name}…</p>}
      {state.phase === "applied" && <p className="panel-note">Imported.</p>}
    </div>
    {state.phase === "refused" && <Refusal problem={state.problem} applied={state.applied} />}
    {shown && <Report report={shown} />}
    {state.phase === "checked" && <div className="button-row">
      {state.report.summary.create + state.report.summary.update > 0 &&
        <button type="button" className="button button--primary" onClick={() => void apply(state.file, state.text, state.report)}>
          Import {state.file.name}
        </button>}
      <button type="button" className="button" onClick={discard}>
        {state.report.summary.create + state.report.summary.update > 0 ? "Cancel" : "Choose another file"}
      </button>
    </div>}
  </Panel>;
}

/**
 * Configuration files on the settings screen (SUB-163): download the export,
 * and import a file in two steps, a dry run that shows what would change and
 * an explicit confirmation that applies it.
 *
 * Editors and administrators only, like the endpoints. The export assigns keys
 * to objects that have none, which is a write.
 */
export function ConfigFilesCard() {
  return <Card title="Import & export" icon={<IconTransfer />}>
    <ExportPanel />
    <ImportPanel />
  </Card>;
}
