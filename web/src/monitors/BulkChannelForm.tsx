import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { Select } from "../components/Select";
import { FieldError } from "../components/FieldError";
import { formatCount } from "../format/format";
import type { Channel } from "../notifications/channels";
import type { ChannelChange, ChannelOperation, ChannelResult } from "./bulkChannelsApi";
import { channelOptionLabel, useChannelOptions } from "./channelChoice";

export type BulkChannelFormProps = {
  selectedIds: readonly string[];
  /** Selected monitors that alert through the default channel today. */
  onDefault: number;
  onChange: ChannelChange;
  onClose: () => void;
  /** Tells the drawer around the form not to close while a request runs. */
  onBusyChange: (busy: boolean) => void;
  /** Injected in tests. Defaults to the real endpoint. */
  load?: (signal: AbortSignal) => Promise<Channel[]>;
};

const monitors = (n: number) => `${formatCount(n)} monitor${n === 1 ? "" : "s"}`;

/**
 * Adds one channel to, or removes it from, the ticked monitors.
 *
 * The same shape as Manage tags, on purpose: preview, then confirm, one
 * atomic write. A channel at a time rather than a set of boxes, because a set
 * for many monitors at once has no honest starting state: boxes ticked "for
 * all of them" would have to say nothing about the monitors that differ, and
 * saving them would replace links nobody looked at. "Add Ops" and "Remove
 * Ops" say exactly what changes, and the server applies them to the links it
 * holds when it writes.
 *
 * The preview names the two routing facts a bulk change can trip over: a
 * remove that leaves monitors with none of their own (the rules or the
 * default take over), and an add that ends the default for monitors that
 * relied on it, or adds a disabled channel that sends nothing.
 */
export function BulkChannelForm({ selectedIds, onDefault, onChange, onClose, onBusyChange, load }: BulkChannelFormProps) {
  const options = useChannelOptions(load);
  const channels = options.phase === "ready" ? options.channels : [];
  const [action, setAction] = useState<ChannelOperation["action"]>("add");
  const [channelId, setChannelId] = useState("");
  const [pending, setPending] = useState(false);
  const busy = useRef(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [preview, setPreview] = useState<{ signature: string; result: ChannelResult } | null>(null);
  const [saved, setSaved] = useState<{ result: ChannelResult; action: ChannelOperation["action"] } | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const resultRef = useRef<HTMLParagraphElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const workingRef = useRef<HTMLParagraphElement>(null);
  const previewRef = useRef<HTMLParagraphElement>(null);
  const channel = channels.find((c) => c.id === channelId);
  const operation: ChannelOperation = {
    action,
    monitor_ids: selectedIds.map(Number),
    channel_id: Number(channelId),
  };
  const signature = JSON.stringify(operation);
  const currentPreview = preview?.signature === signature ? preview.result : null;
  // Disabling the fieldset drops focus to the page; keep it in the dialog.
  useEffect(() => {
    if (pending) workingRef.current?.focus();
    else if (problem) errorRef.current?.focus();
    else if (saved) resultRef.current?.focus();
    else if (currentPreview?.changed === 0) previewRef.current?.focus();
    else if (currentPreview) confirmRef.current?.focus();
  }, [pending, problem, saved, currentPreview]);
  const reset = () => {
    setPreview(null);
    setSaved(null);
    setProblem(null);
  };
  const run = async (commit: boolean) => {
    if (busy.current) return;
    busy.current = true;
    onBusyChange(true);
    setPending(true);
    setProblem(null);
    try {
      const result = await onChange(operation, commit ? currentPreview?.etag : undefined);
      if (commit) {
        setSaved({ result, action });
        setPreview(null);
      } else {
        setPreview({ signature, result });
        setSaved(null);
      }
    } catch (error) {
      setPreview(null);
      setProblem(error instanceof Error ? error.message : "The channel change could not be saved.");
    } finally {
      busy.current = false;
      onBusyChange(false);
      setPending(false);
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void run(false);
  };
  const tooMany = selectedIds.length > 10000;
  const notes = currentPreview === null || channel === undefined ? [] : [
    ...(action === "remove" && currentPreview.leftWithoutOwn > 0
      ? [`${monitors(currentPreview.leftWithoutOwn)} will have no channel of ${currentPreview.leftWithoutOwn === 1 ? "its" : "their"} own left, so alerts go to matching tag routing rules, or else the default channel.`]
      : []),
    ...(action === "add" && onDefault > 0
      ? [`${monitors(onDefault)} alert${onDefault === 1 ? "s" : ""} through the default channel today; with a channel of ${onDefault === 1 ? "its" : "their"} own, the default no longer applies.`]
      : []),
    ...(action === "add" && !currentPreview.channelEnabled
      ? [`${channel.name} is disabled: it sends nothing until it is switched back on, but it still keeps the default channel out.`]
      : []),
  ];
  return (
    <form className="form-column bulk-channels-form" onSubmit={submit}>
      <p>{monitors(selectedIds.length)} selected, including any hidden by filters.</p>
      <fieldset className="add-fieldset" disabled={pending}>
        <legend className="add-legend">Channel change</legend>
        <label className="field">
          <span className="field-label">Action</span>
          <Select className="input" value={action} onChange={(e) => {
            reset();
            setAction(e.target.value as ChannelOperation["action"]);
          }}>
            <option value="add">Add to selected monitors</option>
            <option value="remove">Remove from selected monitors</option>
          </Select>
        </label>
        {options.phase === "loading" && <p className="field-help">Loading channels…</p>}
        {options.phase === "failed" && <FieldError announce={false}>Could not load channels: {options.message}. Close and open this again to retry.</FieldError>}
        {options.phase === "ready" && channels.length === 0 &&
          <p className="field-help">No notification channels exist yet. Add one under Notifications.</p>}
        {channels.length > 0 && (
          <label className="field">
            <span className="field-label">Channel</span>
            <Select className="input" value={channelId} required onChange={(e) => {
              reset();
              setChannelId(e.target.value);
            }}>
              <option value="" disabled>Choose a channel</option>
              {channels.map((c) => <option key={c.id} value={c.id}>{channelOptionLabel(c)}</option>)}
            </Select>
          </label>
        )}
        <p>
          {action === "add"
            ? "Each selected monitor keeps its other channels. Tag routing rules do not change."
            : "Only this channel is detached. Other channels and tag routing rules do not change."}
        </p>
        {tooMany && <p role="alert">Select at most 10,000 monitors for one atomic change. No monitors will be silently skipped.</p>}
        <button type="submit" className="button" disabled={tooMany || selectedIds.length === 0 || channel === undefined}>
          Preview change
        </button>
      </fieldset>
      {pending && <p ref={workingRef} tabIndex={-1} role="status">Working…</p>}
      {problem && <div ref={errorRef} tabIndex={-1}><FieldError>{problem}</FieldError></div>}
      {currentPreview && (
        <div className="bulk-tags-preview" role="status">
          <p ref={previewRef} tabIndex={-1}>
            {monitors(currentPreview.changed)} will change; {formatCount(currentPreview.unchanged)} already{" "}
            {action === "add" ? "have" : "do not have"} it.
          </p>
          {notes.map((note) => <p key={note}>{note}</p>)}
          <button ref={confirmRef} type="button" className="button button--primary"
            disabled={pending || currentPreview.changed === 0} onClick={() => void run(true)}>
            Confirm channel change
          </button>
        </div>
      )}
      {saved && (
        <p ref={resultRef} tabIndex={-1} role="status">
          {saved.action === "add" ? "Added to" : "Removed from"} {monitors(saved.result.changed)}; {formatCount(saved.result.unchanged)} unchanged.
        </p>
      )}
      <button type="button" className="button" aria-disabled={pending}
        // A final tab stop while the fieldset is disabled, as in Manage tags.
        onClick={() => { if (!busy.current) onClose(); }}>
        Close
      </button>
    </form>
  );
}
