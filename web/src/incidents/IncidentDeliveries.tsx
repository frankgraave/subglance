import { useId } from "react";
import { useQuery } from "@tanstack/react-query";
import { StatusChip } from "../components/Chip";
import { Value } from "../components/Value";
import { formatClock } from "../format/format";
import { typeLabel } from "../notifications/channels";
import {
  deliveryChip,
  deliveryWhat,
  fetchIncidentDeliveries,
  incidentDeliveriesQueryKey,
  type IncidentDelivery,
} from "./deliveries";

export type IncidentDeliveriesProps = {
  incidentId: string;
  /** False on a failure that never crossed its threshold: nobody was told. */
  confirmed: boolean;
};

/**
 * Which channels an incident's alerts did and did not reach (SUB-217).
 *
 * It stands in the expanded incident, under the timeline and the error,
 * because it is a fact about this one incident (AGENTS.md: a control or a
 * fact goes with the thing it is about). The notifications screen already
 * says a channel is failing; what it could not say is whether the alert for
 * *this* outage was one of the ones that failed, which is the question asked
 * when somebody says they were never paged.
 *
 * One line per alert per channel, in the timeline's own shape — the time in
 * the same fixed column, then what was sent where, then the outcome as a
 * worded chip — so the panel reads as one more timeline rather than a table
 * with columns of its own. The detail under a line is the failure, the
 * reason it was not sent, or where its news went instead.
 *
 * Loaded when the row opens, not with the incident list: one request per
 * opened row, rather than a delivery list for every incident on the screen
 * that nobody opened.
 */
export function IncidentDeliveries({ incidentId, confirmed }: IncidentDeliveriesProps) {
  const labelId = useId();
  const query = useQuery({
    queryKey: incidentDeliveriesQueryKey(incidentId),
    queryFn: ({ signal }) => fetchIncidentDeliveries(incidentId, signal),
    staleTime: 0,
  });

  return (
    <div className="inc-sent">
      <p className="inc-label" id={labelId}>Notifications</p>
      {query.isPending ? (
        <p className="inc-helper">Loading notifications…</p>
      ) : query.isError ? (
        <p className="inc-helper">
          The notifications for this incident could not be loaded: {query.error.message}
        </p>
      ) : (
        <>
          {query.data.deliveries.length === 0 ? (
            <p className="inc-helper">
              {confirmed
                ? "No notification was sent about this incident."
                : "None: a failure is notified once it is confirmed."}
            </p>
          ) : (
            <ol className="inc-tl" aria-labelledby={labelId}>
              {query.data.deliveries.map((d) => (
                <DeliveryLine key={d.id} delivery={d} />
              ))}
            </ol>
          )}
          {/*
           * Said when the outbox may have pruned some of this incident's
           * rows, so a short list is not read as a complete one.
           */}
          {query.data.complete ? null : (
            <p className="inc-helper">
              Delivered and skipped notifications are kept for{" "}
              {query.data.windowDays} days, so earlier ones may be missing.
            </p>
          )}
        </>
      )}
    </div>
  );
}

function DeliveryLine({ delivery }: { delivery: IncidentDelivery }) {
  const chip = deliveryChip(delivery.outcome);
  const channel =
    delivery.channelName === ""
      ? "a deleted channel"
      : `${delivery.channelName} (${typeLabel(delivery.channelType)})`;
  return (
    <li className="inc-tl-step">
      <Value className="inc-tl-at">{formatClock(delivery.at) ?? "—"}</Value>
      <div className="inc-sent-what">
        <p>
          {deliveryWhat(delivery.event)} to {channel}{" "}
          <StatusChip status={chip.status}>{chip.word}</StatusChip>
        </p>
        {delivery.detail === null ? null : (
          <p className="inc-helper">{delivery.detail}</p>
        )}
      </div>
    </li>
  );
}
