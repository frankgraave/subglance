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
 * when that union is empty (SUB-147, SUB-124). A disabled channel still
 * counts toward that union, so it keeps the default out, but the sender
 * skips it: a route made only of disabled channels reaches nobody. The
 * sentence exists so nobody has to know that to read the form: it names who
 * does hear about the monitor, and when nobody would, it says so, as a
 * warning.
 *
 * `chosen` are the channels ticked here, `all` the instance's channels, and
 * `rules` the ones the monitor's saved tags match; a monitor being added has
 * none yet.
 */
export function describeRouting(
  chosen: readonly Channel[],
  rules: readonly RuleRoute[],
  all: readonly Channel[],
): { text: string; warn: boolean } | null {
  const known = (id: string) => all.find((channel) => channel.id === id);
  const routed = rules.flatMap((rule) =>
    rule.ids.flatMap((id, index) => (known(id)?.enabled ? [`${rule.names[index]} via ${rule.tag}`] : [])),
  );
  const live = chosen.filter((channel) => channel.enabled).length;
  if (routed.length > 0) {
    const list = routed.join(", ");
    if (live > 0) return { text: `Alerts also go to ${list}, from a tag routing rule.`, warn: false };
    return chosen.length === 0
      ? { text: `With none chosen, alerts go to ${list}, from a tag routing rule.`, warn: false }
      : { text: `Nothing chosen here is enabled, so alerts go only to ${list}, from a tag routing rule.`, warn: false };
  }
  const def = all.find((channel) => channel.isDefault);
  if (live > 0) {
    return def === undefined
      ? null
      : { text: `The default channel, ${def.name}, is not used while a channel is chosen here.`, warn: false };
  }
  if (chosen.length > 0 || rules.some((rule) => rule.ids.some((id) => known(id) !== undefined))) {
    return { text: "Every channel this monitor is routed to is disabled, so nobody is alerted about it.", warn: true };
  }
  if (def === undefined) {
    return { text: "With none chosen and no default channel, nobody is alerted about this monitor.", warn: true };
  }
  return def.enabled
    ? { text: `With none chosen, alerts go to the default channel, ${def.name}.`, warn: false }
    : { text: `With none chosen, alerts go to the default channel, ${def.name}, which is disabled, so nobody is alerted about this monitor.`, warn: true };
}
