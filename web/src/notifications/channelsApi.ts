/**
 * The requests the notifications page makes.
 *
 * Apart from the components for the same reason `monitors/inventoryApi.ts` is:
 * the screen renders a model and knows nothing about fetching, so a test can
 * drive every state from a fixture. Everything goes through `apiFetch` or
 * `apiRequest`, so an expired session reaches the session owner in one place
 * instead of surfacing as "HTTP 401" on a row.
 */

import { apiFetch, apiRequest } from "../api/http";
import { channelsFromPayload, channelFromApi } from "./channels";
import type { ApiChannel, Channel, QuietHours } from "./channels";

export const channelsQueryKey = ["notifications", "channels"] as const;

/** Every configured channel. */
export async function fetchChannels(signal?: AbortSignal): Promise<Channel[]> {
  const res = await apiFetch("/api/v1/channels", { signal });
  if (!res.ok) {
    throw new Error(`could not load channels: HTTP ${res.status}`);
  }
  const body = (await res.json()) as { channels?: ApiChannel[] };
  /*
   * A 200 whose body has no channel list is a failure, not an empty instance.
   *
   * "Alerts are going nowhere" is the loudest claim this page can make, and on
   * an instance with four working channels it would send someone to rebuild
   * configuration that was never lost. It is never said on the strength of a
   * body we could not read.
   */
  if (!Array.isArray(body?.channels)) {
    throw new Error(
      "could not load channels: the server's reply had no channel list in it",
    );
  }
  return channelsFromPayload(body);
}

/** The fields a create or edit sends. `config` replaces the stored map. */
export type ChannelInput = {
  name: string;
  type: string;
  config: Record<string, string>;
  enabled?: boolean;
};

export async function createChannel(
  input: ChannelInput,
  signal?: AbortSignal,
): Promise<Channel> {
  const res = await apiRequest("/api/v1/channels", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal,
  });
  return channelFromApi((await res.json()) as ApiChannel);
}

/**
 * Replaces a channel definition.
 *
 * PUT, not PATCH, because that is what the server offers — and the whole
 * object has to be sent, which is precisely why an untouched secret must be
 * sent back as the mask it arrived as. `handleUpdateChannel` recognises a
 * value that still equals its own mask and restores the stored credential;
 * sending an empty string instead would destroy it, and sending nothing at all
 * would fail validation for the types whose only required field is the secret.
 */
export async function updateChannel(
  id: string,
  input: ChannelInput,
  signal?: AbortSignal,
): Promise<Channel> {
  const res = await apiRequest(`/api/v1/channels/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
    signal,
  });
  return channelFromApi((await res.json()) as ApiChannel);
}

/** Deletes one channel. The server answers 204; there is nothing to read. */
export async function deleteChannel(
  id: string,
  signal?: AbortSignal,
): Promise<void> {
  await apiRequest(`/api/v1/channels/${encodeURIComponent(id)}`, {
    method: "DELETE",
    signal,
  });
}

/**
 * Makes a channel the default, or stops it being one.
 *
 * Clearing names the channel rather than "whatever the default is", which is
 * what the server offers for a reason: a page drawn before someone else chose
 * a new default must not be able to unset theirs.
 */
export async function setDefaultChannel(
  id: string,
  isDefault: boolean,
  signal?: AbortSignal,
): Promise<void> {
  await apiRequest(`/api/v1/channels/${encodeURIComponent(id)}/default`, {
    method: isDefault ? "PUT" : "DELETE",
    signal,
  });
}

/**
 * Sets, replaces or removes a channel's quiet hours.
 *
 * A separate resource from the channel on the server, so a separate request
 * here. Null clears the window, and the server then releases anything it was
 * holding at once.
 */
export async function setQuietHours(
  id: string,
  quiet: QuietHours | null,
  signal?: AbortSignal,
): Promise<void> {
  await apiRequest(`/api/v1/channels/${encodeURIComponent(id)}/quiet-hours`, {
    method: quiet === null ? "DELETE" : "PUT",
    ...(quiet === null
      ? {}
      : {
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(quiet),
        }),
    signal,
  });
}

/** What one real test delivery reported. */
export type TestOutcome = { ok: true } | { ok: false; error: string };

/**
 * Sends a real message through one channel.
 *
 * `POST /channels/{id}/test` answers 200 `{ok:true}` or 502 `{ok:false,error}`,
 * and the error is the upstream's own sentence. Both are reported verbatim.
 *
 * The failure path does not go through `apiRequest`: that would turn the 502
 * into a thrown `ApiError` and the caller would have to reconstruct "the test
 * ran and the far end refused" from an exception. A refused delivery is a
 * result, not a transport failure, and the distinction is what stops the row
 * saying "could not send test" when what happened is "Slack said 404".
 */
export async function testChannel(
  id: string,
  signal?: AbortSignal,
): Promise<TestOutcome> {
  const res = await apiFetch(
    `/api/v1/channels/${encodeURIComponent(id)}/test`,
    { method: "POST", signal },
  );
  let body: { ok?: boolean; error?: string } = {};
  try {
    body = (await res.json()) as { ok?: boolean; error?: string };
  } catch {
    // A non-JSON body (a proxy's HTML 502) leaves the status line, which is
    // still more use than throwing a parse error over the top of it.
  }
  if (res.ok && body.ok === true) return { ok: true };
  /*
   * Anything else is a failure, including a 200 whose body does not say ok.
   *
   * The direction matters: reading an unrecognised reply as success would put
   * a green tick on a channel nobody proved anything about, which is the exact
   * lie this button exists to prevent.
   */
  const error =
    body.error !== undefined && body.error !== ""
      ? body.error
      : `the server rejected the test (HTTP ${res.status}) without saying why`;
  return { ok: false, error };
}
