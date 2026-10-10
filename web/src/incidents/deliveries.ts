/**
 * Where one incident's alerts went: the request and the words for it.
 *
 * Imported only by `IncidentDeliveries.tsx`, so it travels in that lazy chunk
 * rather than in the entry: nobody needs it until a row is opened.
 */

import { apiFetch } from "../api/http";

/** What became of one alert on one channel; see the API's `state`. */
export type DeliveryOutcome =
  | "delivered"
  | "failed"
  | "retrying"
  | "queued"
  | "held"
  | "not_sent"
  | "merged";

export type IncidentDelivery = {
  id: string;
  /** Empty when the channel has since been deleted. */
  channelName: string;
  channelType: string;
  event: string;
  outcome: DeliveryOutcome;
  /** When it reached its outcome, or was queued while it is still going out. */
  at: number | null;
  /** The failure, the reason it was not sent, or what carried it. */
  detail: string | null;
};

export type IncidentDeliveries = {
  deliveries: IncidentDelivery[];
  windowDays: number;
  /** False when the incident is older than the outbox keeps its rows. */
  complete: boolean;
};

type ApiDelivery = {
  id: number;
  channel_name: string;
  channel_type: string;
  event: string;
  state: string;
  queued_at: string;
  ended_at: string | null;
  error: string;
  reason: string;
  merged_into?: string;
};

const OUTCOMES: readonly DeliveryOutcome[] = [
  "delivered",
  "failed",
  "retrying",
  "queued",
  "held",
  "not_sent",
  "merged",
];

export const incidentDeliveriesQueryKey = (incidentId: string) =>
  ["incidents", incidentId, "deliveries"] as const;

/** "Dropped during quiet hours", from the server's lower-case reason. */
function sentence(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function detailOf(api: ApiDelivery, outcome: DeliveryOutcome): string | null {
  switch (outcome) {
    case "failed":
    case "retrying":
      return api.error === "" ? null : api.error;
    case "not_sent":
      return api.reason === "" ? null : sentence(api.reason);
    case "held":
      return "Waiting for the channel's quiet hours to end.";
    case "merged":
      return api.merged_into === "recovery"
        ? "The monitor was back up before it went out, so the recovery message carried it."
        : "Folded into the quiet-hours digest, which is listed here too.";
    default:
      return null;
  }
}

export function deliveryFromApi(api: ApiDelivery): IncidentDelivery {
  /*
   * A state this build does not know is shown as queued rather than
   * delivered: a list that answers "did anyone hear about this?" must never
   * guess the good answer.
   */
  const outcome = (OUTCOMES as readonly string[]).includes(api.state)
    ? (api.state as DeliveryOutcome)
    : "queued";
  const at = Date.parse(api.ended_at ?? api.queued_at);
  return {
    id: String(api.id),
    channelName: api.channel_name,
    channelType: api.channel_type,
    event: api.event,
    outcome,
    at: Number.isFinite(at) ? at : null,
    detail: detailOf(api, outcome),
  };
}

export async function fetchIncidentDeliveries(
  incidentId: string,
  signal?: AbortSignal,
): Promise<IncidentDeliveries> {
  const res = await apiFetch(
    `/api/v1/incidents/${encodeURIComponent(incidentId)}/deliveries`,
    { signal },
  );
  if (!res.ok) {
    throw new Error(`could not load notifications: HTTP ${res.status}`);
  }
  const body = (await res.json()) as {
    deliveries?: ApiDelivery[];
    window_days?: number;
    complete?: boolean;
  };
  return {
    deliveries: (body.deliveries ?? []).map(deliveryFromApi),
    windowDays: body.window_days ?? 30,
    complete: body.complete !== false,
  };
}

/** What the message was, as a word. */
export function deliveryWhat(event: string): string {
  switch (event) {
    case "incident_confirmed":
      return "Alert";
    case "incident_reminder":
      return "Reminder";
    case "incident_resolved":
      return "Recovery";
    case "quiet_hours_digest":
      return "Quiet-hours digest";
    default:
      return "Message";
  }
}

/** The chip: a word, and the colour of the news it carries. */
export function deliveryChip(outcome: DeliveryOutcome): {
  status: "up" | "warn" | "down" | "idle";
  word: string;
} {
  switch (outcome) {
    case "delivered":
      return { status: "up", word: "Delivered" };
    case "failed":
      return { status: "down", word: "Failed" };
    case "retrying":
      return { status: "warn", word: "Retrying" };
    case "held":
      return { status: "idle", word: "Held" };
    case "not_sent":
      return { status: "idle", word: "Not sent" };
    case "merged":
      return { status: "idle", word: "Merged" };
    default:
      return { status: "idle", word: "Queued" };
  }
}
