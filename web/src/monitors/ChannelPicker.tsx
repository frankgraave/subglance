import { useId } from "react";
import { Checkbox } from "../components/Choice";
import type { Channel } from "../notifications/channels";
import type { RuleRoute } from "./inventory";
import { channelOptionLabel, describeRouting, useChannelOptions } from "./channelChoice";
import { FieldError } from "../components/FieldError";

export type ChannelPickerProps = {
  /** The chosen channel ids. */
  value: readonly string[];
  onChange: (ids: string[]) => void;
  /** The routing rules the monitor's saved tags match; none for a new one. */
  rules?: readonly RuleRoute[];
  /** A rejection the server placed on `channel_ids`. */
  error?: string;
  /** What a failed channel read means for this form's save. */
  failedNote: string;
  /** Injected in tests. Defaults to the real endpoint. */
  load?: (signal: AbortSignal) => Promise<Channel[]>;
};

/**
 * A monitor's own channels, chosen in its add and edit forms (SUB-179).
 *
 * Checkboxes in a fieldset rather than a multi-select: every option and its
 * state is visible at once, which a `<select multiple>` hides behind a
 * modifier key nobody discovers, and a group of checkboxes under one legend
 * is how WAI's form tutorial groups a several-of-many choice. An instance
 * has a handful of channels, so the list stays short.
 *
 * Under it, one sentence says who hears about the monitor as the boxes
 * stand, so the default channel is visible as the fallback when nothing is
 * ticked, and a monitor that would alert nobody says so before it is saved.
 */
export function ChannelPicker({ value, onChange, rules = [], error, failedNote, load }: ChannelPickerProps) {
  const ids = useId();
  const options = useChannelOptions(load);
  const chosen = new Set(value);
  const channels = options.phase === "ready" ? options.channels : [];
  // Only ids still on the list survive a change: a channel deleted since the
  // monitor was read is detached already, and sending it back would be
  // refused as unknown.
  const toggle = (id: string, on: boolean) => {
    const kept = value.filter((v) => v !== id && channels.some((c) => c.id === v));
    onChange(on ? [...kept, id] : kept);
  };
  const routing = options.phase === "ready" && channels.length > 0
    ? describeRouting(channels.filter((c) => chosen.has(c.id)), rules, channels)
    : null;
  const described = [
    ...(routing !== null || (options.phase === "ready" && channels.length === 0) ? [`${ids}-help`] : []),
    ...(error !== undefined ? [`${ids}-error`] : []),
  ].join(" ");
  return <fieldset className="field mon-channels" aria-describedby={described || undefined}>
    <legend className="field-label">Channels</legend>
    {options.phase === "loading" && <p className="field-help">Loading channels…</p>}
    {/* Not an alert: nothing the user did failed, and the form still works. */}
    {options.phase === "failed" && <FieldError announce={false}>Could not load channels: {options.message}. {failedNote}</FieldError>}
    {options.phase === "ready" && channels.length === 0 &&
      <p id={`${ids}-help`} className="warn-note">No notification channels exist yet, so nobody is alerted about this monitor. Add one under Notifications.</p>}
    {channels.length > 0 && <div className="mon-channels-list">
      {channels.map((channel) => <Checkbox key={channel.id} value={channel.id} data-channel-input=""
        checked={chosen.has(channel.id)} onChange={(event) => toggle(channel.id, event.target.checked)}
        aria-invalid={error === undefined ? undefined : true}>{channelOptionLabel(channel)}</Checkbox>)}
    </div>}
    {routing !== null && <p id={`${ids}-help`} className={routing.warn ? "warn-note" : "field-help"}>{routing.text}</p>}
    {error !== undefined && <FieldError id={`${ids}-error`}>{error}</FieldError>}
  </fieldset>;
}
