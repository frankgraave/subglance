import { memo } from "react";
import { Led } from "./Led";
import { MonitorLink } from "./MonitorLink";
import { Value } from "../components/Value";
import { StateChip } from "../components/Chip";
import { formatDuration } from "./detail";
import { describeTarget, statusWord } from "./format";
import {
  canCheckNow,
  describeChannels,
  intervalOf,
  typeLabel,
} from "./inventory";
import type { ChannelState, InventoryMonitor } from "./inventory";
import type { CheckOutcome } from "./inventoryApi";

/**
 * One monitor as one inventory row.
 *
 * The shape is the approved mockup's: name and target take the elastic column
 * and clip; every setting after them is a fixed-width column, so the eye can
 * read straight *down* "Every" and find the monitor checked once an hour, or
 * down "Channels" and find the one nobody will hear about. That downward scan
 * is the whole reason the page exists — it finds misconfiguration, where the
 * dashboard finds outages.
 *
 * **Every action is inline and present on every row.** No hover-only `⋯` menu:
 * a menu that hides Pause next to Delete behind a pointer is unusable on a
 * phone and dangerous everywhere else. The actions are ordinary buttons with
 * their words in them, so each one has an accessible name without an
 * `aria-label` that could drift from the label beside it.
 *
 * **Status is never only the lamp.** `Led` carries the word for a screen
 * reader, the paused row additionally says PAUSED in a chip that is read out,
 * and a check result is a sentence. Nothing on this row means anything by
 * colour alone (DESIGN.md §2.3).
 *
 * Memoised: an instance with a few hundred monitors renders a few hundred of
 * these, and a pause on one must not re-render the rest.
 */

export type MonitorInventoryRowProps = {
  monitor: InventoryMonitor;
  /** Which channels it alerts through, or that we could not find out. */
  channels: ChannelState;
  /** Opens this monitor's detail view client-side. */
  onOpen?: (id: string) => void;
  /** Opens the edit drawer. Absent for a viewer, who may not write. */
  onEdit?: (id: string) => void;
  /** Pauses or resumes. Absent for a viewer. */
  onTogglePaused?: (id: string, paused: boolean) => void;
  /** Runs a check now. Absent for a viewer, and never called for push. */
  onCheckNow?: (id: string) => void;
  /** Asks for the delete confirmation. Absent for a viewer. */
  onDelete?: (id: string) => void;
  /** True while this row's pause/resume is in flight. */
  busy?: boolean;
  /** True while this row's manual check is in flight. */
  checking?: boolean;
  /** The last manual check this row ran, if any. */
  checkResult?: CheckOutcome | null;
  /** A write on this row that failed, as the server explained it. */
  rowError?: string | null;
};

function MonitorInventoryRowImpl({
  monitor,
  channels,
  onOpen,
  onEdit,
  onTogglePaused,
  onCheckNow,
  onDelete,
  busy = false,
  checking = false,
  checkResult = null,
  rowError = null,
}: MonitorInventoryRowProps) {
  const paused = !monitor.enabled;
  const checkable = canCheckNow(monitor);
  const channelText = describeChannels(channels);
  /*
   * Every row action carries the monitor's name in its accessible name.
   *
   * Two hundred buttons all called "Pause" is a list a screen reader user
   * cannot navigate and a voice-control user cannot address. It is an
   * `aria-label` rather than an `sr-only` span because the accessible name is
   * computed by concatenating descendant text with no separator, so a hidden
   * span next to the word produces "Pauseauth" — and a non-breaking space in
   * it is trimmed away rather than preserved.
   *
   * Each label is built from the same string that is rendered, so the name and
   * the visible word cannot drift apart, and the visible word is always a
   * prefix of the name (WCAG 2.5.3, label in name).
   */
  const pauseWord = busy ? "Working…" : paused ? "Resume" : "Pause";
  const checkWord = checking ? "Checking…" : "Check now";
  const tags = Object.entries(monitor.tags);

  return (
    <li className="inv-row" data-paused={paused ? "true" : "false"}>
      <div className="inv-line">
        {/* The word is always rendered, never only the colour. It is visually
            hidden because the column is three pixels wide and a printed
            status word on every one of two hundred rows would drown the
            settings the page is actually for — but it is in the accessible
            name of nothing and in the row's text for everyone using a screen
            reader. */}
        <Led status={monitor.status} className="inv-led" />

        <div className="inv-main">
          <span className="inv-name">
            <MonitorLink id={monitor.id} name={monitor.name} onOpen={onOpen} />
            {paused ? (
              /* A configuration state, not a health state, so it gets no
                 status colour — a dashed state chip, which in this product
                 already means "about the data rather than the data". The
                 dimming of the row is the second signal, never the only
                 one. */
              <StateChip className="inv-paused-chip">Paused</StateChip>
            ) : null}
          </span>
          <span className="inv-sub">{describeTarget(monitor)}</span>
        </div>

        <div className="inv-meta">
          <span className="inv-col inv-col--type">
            <span className="inv-label">Type</span>
            <span className="inv-type">{typeLabel(monitor.type)}</span>
          </span>

          <span className="inv-col inv-col--num">
            <span className="inv-label">Every</span>
            <Value value={intervalOf(monitor)}>
              {formatDuration(intervalOf(monitor))}
            </Value>
          </span>

          <span className="inv-col inv-col--num">
            <span className="inv-label">Timeout</span>
            {monitor.timeoutS === null ? (
              /* Not a dash: a push monitor has no timeout because nothing is
                 dialled, and an em dash here would read as "we failed to load
                 it". The word says which. */
              <Value>
                <span className="inv-na">n/a</span>
              </Value>
            ) : (
              <Value value={monitor.timeoutS}>
                {formatDuration(monitor.timeoutS)}
              </Value>
            )}
          </span>

          <span className="inv-col inv-col--chan">
            <span className="inv-label">Channels</span>
            {/* `none` is a finding and renders in the dim zero ink; `not
                loaded` is an admission and renders as a dashed state chip, so
                the two can never be mistaken for each other. The emptiness is
                the discovery this column exists for — inventing it out of a
                failed request would send someone hunting a bug that is not
                there. */}
            {!channels.known ? (
              <StateChip>not loaded</StateChip>
            ) : (
              <Value value={channels.names.length === 0 ? 0 : undefined}>
                {channelText}
              </Value>
            )}
          </span>

          <span className="inv-col inv-col--tags">
            <span className="inv-label">Tags</span>
            {tags.length === 0 ? (
              <Value value={0}>none</Value>
            ) : (
              <span className="inv-tags">
                {tags.map(([key, value]) => (
                  <span key={key} className="inv-tag">
                    {key}:{value}
                  </span>
                ))}
              </span>
            )}
          </span>
        </div>

        <div className="inv-acts">
          {onCheckNow !== undefined && (
            <button
              type="button"
              className="add-button inv-act"
              onClick={() => onCheckNow(monitor.id)}
              disabled={!checkable || checking}
              aria-label={
                checkable
                  ? `${checkWord} ${monitor.name}`
                  : `${checkWord} ${monitor.name} — unavailable for a push monitor`
              }
              /* The reason a control is disabled has to be readable, or the
                 row is a dead button with no explanation. A push monitor is
                 not probed by SubGlance at all, which is a fact about the
                 monitor rather than about permissions. */
              title={
                checkable
                  ? undefined
                  : "A push monitor is reported to, not probed — your job calls SubGlance"
              }
            >
              {checkWord}
            </button>
          )}

          {onTogglePaused !== undefined && (
            <button
              type="button"
              className="add-button inv-act"
              onClick={() => onTogglePaused(monitor.id, !paused)}
              disabled={busy}
              aria-label={`${pauseWord} ${monitor.name}`}
            >
              {pauseWord}
            </button>
          )}

          {onEdit !== undefined && (
            <button
              type="button"
              className="add-button inv-act"
              onClick={() => onEdit(monitor.id)}
              aria-label={`Edit ${monitor.name}`}
            >
              Edit
            </button>
          )}

          {onDelete !== undefined && (
            <button
              type="button"
              className="add-button inv-act inv-act--danger"
              onClick={() => onDelete(monitor.id)}
              aria-label={`Delete ${monitor.name}`}
            >
              Delete
            </button>
          )}
        </div>
      </div>

      {/*
       * The result of a manual check, next to the row it is about.
       *
       * Not a toast: the answer to "is it back yet" has to stay on screen
       * beside the monitor it describes, and a toast takes it away on a timer
       * (DESIGN.md §7.6). `role="status"` so it is announced without stealing
       * focus from the button that was just pressed.
       */}
      {checkResult !== null && (
        <p className="inv-result" role="status">
          {checkResult.ok ? "Check passed" : "Check failed"}
          {checkResult.statusCode !== undefined
            ? ` — HTTP ${checkResult.statusCode}`
            : ""}
          {` in ${checkResult.latencyMs} ms.`}
          {checkResult.error !== undefined ? ` ${checkResult.error}` : ""}
          {/* A paused monitor's manual check is deliberately not recorded by
              the server, and silence about that makes a green result look
              like a dashboard that failed to update.
              Past tense, and deliberately so: the result outlives the pause.
              Resuming the monitor refetches the row but does not erase a check
              that already ran, so "this monitor is paused" would go on being
              said about a monitor that is now running. What the sentence is
              actually about is the check, not the current state. */}
          {checkResult.recorded
            ? ""
            : " Not recorded — the monitor was paused when this check ran, so the result is not part of its history."}
        </p>
      )}

      {rowError !== null && (
        <p className="inv-result inv-result--bad" role="alert">
          {rowError}
        </p>
      )}

      <span className="sr-only">
        {/* The row's facts as one sentence, so a screen reader gets the same
            page as the eye does rather than a run of unlabelled columns. The
            status word is here rather than only in the lamp because this is
            the sentence that is actually read. */}
        Status: {statusWord(monitor.status)}. Checked every{" "}
        {formatDuration(intervalOf(monitor))}. Channels: {channelText}.
      </span>
    </li>
  );
}

export const MonitorInventoryRow = memo(MonitorInventoryRowImpl);
