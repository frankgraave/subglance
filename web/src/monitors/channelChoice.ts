/**
 * What the monitor form's channel picker says and loads (SUB-179).
 *
 * Kept apart from `ChannelPicker.tsx` so the sentences, which are the
 * decision, can be tested without a renderer, and because the lint rule for
 * fast refresh wants a component file to export components only.
 */

import { useEffect, useRef, useState } from "react";
import { fetchChannels } from "../notifications/channelsApi";
import type { Channel } from "../notifications/channels";
import { typeLabel } from "../notifications/channels";
import type { RuleRoute } from "./inventory";

/** The channel list, or why there is none. */
export type ChannelOptions =
  | { phase: "loading" }
  | { phase: "failed"; message: string }
  | { phase: "ready"; channels: readonly Channel[] };

/**
 * Loads every channel once per mount of the form.
 *
 * Its own read rather than the notifications page's cached query: a form is
 * opened to decide something, and a channel deleted or renamed since that
 * page last loaded is exactly what must not be offered. Aborted on unmount,
 * so a closed drawer cannot write into the next one.
 */
export function useChannelOptions(
  load: (signal: AbortSignal) => Promise<Channel[]> = fetchChannels,
): ChannelOptions {
  const [state, setState] = useState<ChannelOptions>({ phase: "loading" });
  // Read once, at mount: a caller handing in a new function every render
  // must not turn one read into a loop.
  const loader = useRef(load);
  useEffect(() => {
    const controller = new AbortController();
    loader.current(controller.signal).then((channels) => {
      if (!controller.signal.aborted) setState({ phase: "ready", channels });
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setState({ phase: "failed", message: error instanceof Error ? error.message : "could not load channels" });
    });
    return () => controller.abort();
  }, []);
  return state;
}

/**
 * How one channel is named in the list.
 *
 * The type travels with the name because a name is not an identity: two
 * channels may share one, and "Ops" twice is a choice nobody can make. A
 * disabled channel says so, since choosing it sends nothing until it is
 * switched back on.
 */
export function channelOptionLabel(channel: Channel): string {
  return [
    channel.name,
    typeLabel(channel.type),
    ...(channel.isDefault ? ["default"] : []),
    ...(channel.enabled ? [] : ["disabled"]),
  ].join(" · ");
}

/** The ids as the form holds them: sorted, so order is never a change. */
export function channelIdsText(ids: readonly string[]): string {
  return [...ids].sort((a, b) => Number(a) - Number(b)).join(",");
}

export function channelIdsFromText(text: string): string[] {
  return text === "" ? [] : text.split(",");
}

/**
 * Where an alert goes besides, or instead of, the channels ticked.
 *
 * The routing rules are the server's: alerts go to the union of a monitor's
 * own channels and every rule its tags match, and the default stands in only
 * when that union is empty (SUB-147, SUB-124). The sentence exists so nobody
 * has to know that to read the form: with nothing ticked, it names who does
 * hear about the monitor, and when nobody would, it says so, as a warning.
 *
 * `rules` are the ones the monitor's saved tags match; a monitor being added
 * has none yet.
 */
export function describeRouting(
  chosen: number,
  rules: readonly RuleRoute[],
  defaultName: string | undefined,
): { text: string; warn: boolean } | null {
  const routed = rules.flatMap((rule) => rule.names.map((name) => `${name} via ${rule.tag}`));
  if (routed.length > 0) {
    const list = routed.join(", ");
    return chosen === 0
      ? { text: `With none chosen, alerts go to ${list}, from a tag routing rule.`, warn: false }
      : { text: `Alerts also go to ${list}, from a tag routing rule.`, warn: false };
  }
  if (chosen > 0) {
    return defaultName === undefined
      ? null
      : { text: `The default channel, ${defaultName}, is not used while a channel is chosen here.`, warn: false };
  }
  return defaultName === undefined
    ? { text: "With none chosen and no default channel, nobody is alerted about this monitor.", warn: true }
    : { text: `With none chosen, alerts go to the default channel, ${defaultName}.`, warn: false };
}
