/**
 * An incident told as a sentence, not as a log line.
 *
 * SUB-34 asks for one thing: the incident list should be readable without the
 * reader having to work out what happened. "down since 14:03, 12 minutes, DNS
 * failure, recovered at 14:15" is the shape — a beginning, a length, a reason
 * and an ending, in the order a person would say them out loud.
 *
 * All of it is pure string work, deliberately apart from React, for the same
 * reason `format.ts` is: the wording is the product decision here, and it
 * deserves a test that does not need a renderer. Two screens render these
 * sentences — the per-monitor history and the all-monitors incidents view —
 * and a sentence built twice is a sentence that will eventually disagree with
 * itself.
 *
 * ---
 *
 * **The decision this file exists to protect: acknowledging does not close
 * anything.**
 *
 * Acking says "seen, working on it". It stops the escalating repeat
 * notifications (15 min, 1 h, 4 h, 16 h, then daily) and it changes nothing
 * about whether the service is answering. The server models this honestly —
 * `acked_at` and `resolved_at` are separate columns, and `handleAckIncident`
 * writes only the first — and the UI has to model it just as honestly, because
 * a row that reads as finished while the service is still down is precisely
 * the kind of comfortable untruth this product exists to avoid.
 *
 * So there are **three** states here and never two. `acked` is a state of the
 * *response*, not of the incident, and its wording keeps the outage in the
 * present tense: "Down since 14:03, 12 min and counting. Acknowledged at
 * 14:10 — still down, repeat alerts paused."
 */

import { formatDuration, formatMoment } from "../monitors/detail";
import type { Incident } from "../monitors/detail";

/**
 * The three states an incident row can be in.
 *
 * Not a boolean pair. `resolved && acked` is possible on the wire — somebody
 * acked an outage that later recovered — and it is history either way, so it
 * collapses to `resolved`. What must never collapse is `acked` into
 * `resolved`: see the note at the top of the file.
 */
export type IncidentState = "open" | "acked" | "resolved";

export function incidentState(incident: Incident): IncidentState {
  if (incident.resolved) return "resolved";
  return incident.acked ? "acked" : "open";
}

/**
 * The badge in the row's response column.
 *
 * **It states the response, not the service.** The row already says whether
 * the service is answering, in three other places: the lamp, the row's rail,
 * and a duration that is still counting. This column answers the separate
 * question "is anybody on it" — which is exactly the separation the whole
 * ticket turns on, and drawing it as a second column rather than as one
 * combined word is what makes the two states impossible to conflate.
 *
 * The acked word still carries "still down" rather than reading
 * "Acknowledged". On its own that label is the one that would make a
 * still-broken service look handled, and a badge is not allowed to be shorter
 * than the truth just because its column is narrow.
 *
 * The stale column is SUB-111 applied here: once the live stream is dead, no
 * view states anything in the present tense, and CSS cannot reach a word.
 */
export const STATE_BADGE: Record<IncidentState, string> = {
  open: "Unacked",
  acked: "Acked, still down",
  resolved: "Resolved",
};

export const STATE_BADGE_LAST_KNOWN: Record<IncidentState, string> = {
  open: "Was unacked",
  acked: "Acked, was still down",
  resolved: "Resolved",
};

export const stateBadge = (state: IncidentState, stale = false): string =>
  stale ? STATE_BADGE_LAST_KNOWN[state] : STATE_BADGE[state];

/**
 * Which chip colour each state takes.
 *
 * `acked` is `warn`, not `up`: it is the middle state, and giving it the
 * healthy colour would say "fixed" in the one channel that reads fastest.
 * `resolved` is `idle` rather than `up` for the reason `detail.css` already
 * documents about resolved rows — a column of green "resolved" badges from
 * last month reads, at a glance, as a wall of current health.
 */
export const STATE_TONE: Record<IncidentState, "down" | "warn" | "idle"> = {
  open: "down",
  acked: "warn",
  resolved: "idle",
};

/**
 * A failure kind as a person would say it.
 *
 * The server stores the checker's `FailureKind` — `dns`, `cert_expiry`,
 * `push_overdue` — which is a good key and a poor sentence. The ticket's
 * example says "DNS error", not "dns", and that difference is the whole point
 * of the ticket.
 *
 * An unknown kind is passed through rather than dropped or replaced with
 * "unknown": a cause this build has not heard of is still more information
 * than no cause, and a newer server adding one must not make its incidents
 * read as causeless.
 */
const CAUSE_WORDS: Record<string, string> = {
  dns: "DNS failure",
  connection: "connection refused",
  tls: "TLS failure",
  timeout: "timed out",
  status: "unexpected status code",
  keyword: "keyword missing",
  cert_expiry: "certificate expiring",
  internal: "internal error",
  push_overdue: "no report received",
  push_reported: "the job reported a failure",
};

export function causeWords(cause: string | undefined): string | null {
  if (cause === undefined || cause === "") return null;
  return CAUSE_WORDS[cause] ?? cause;
}

/** A wall-clock time, for an ending that shares a day with its beginning. */
export function formatClock(ms: number | null): string | null {
  if (ms === null) return null;
  const date = new Date(ms);
  if (Number.isNaN(date.getTime())) return null;
  return date.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
  });
}

function sameDay(a: number, b: number): boolean {
  const x = new Date(a);
  const y = new Date(b);
  return (
    x.getFullYear() === y.getFullYear() &&
    x.getMonth() === y.getMonth() &&
    x.getDate() === y.getDate()
  );
}

/**
 * An ending, dated only as much as it needs to be.
 *
 * An outage that started and finished this afternoon ends "at 14:15"; one that
 * ran overnight gets the full date back, because "recovered at 02:40" without
 * a day is a sentence that quietly loses twenty-four hours.
 */
function endingMoment(startedAt: number | null, endedAt: number): string | null {
  if (startedAt !== null && sameDay(startedAt, endedAt)) {
    return formatClock(endedAt);
  }
  return formatMoment(endedAt);
}

export type IncidentStory = {
  state: IncidentState;
  /** The badge word, already in the right tense. */
  badge: string;
  /** "Down since 15 Nov 2023, 14:03" — beginning, always absolute. */
  began: string;
  /** "12 min", or "12 min and counting" while it is still open. */
  lasted: string;
  /**
   * The same length in seconds, for a caller that wants the bare number in a
   * narrow column. Exposed rather than recomputed, so the column and the
   * sentence beside it can never disagree about how long an outage has run.
   */
  durationS: number;
  /** "DNS failure", or null when the server classified nothing. */
  cause: string | null;
  /** "Recovered at 14:15", or null while the incident is open. */
  ended: string | null;
  /**
   * What acknowledging did, in the words that keep it apart from resolving.
   * Null when nobody has acked, so the caller can offer the button instead.
   */
  acked: string | null;
  /**
   * The whole story as one sentence.
   *
   * This is what a screen reader hears, and it is assembled from exactly the
   * same parts the eye is shown — no more and no less. The guard against a
   * visual-only distinction between "acknowledged" and "resolved" is not a
   * convention here; it is this field.
   */
  sentence: string;
};

/**
 * The story of one incident.
 *
 * `now` is a parameter rather than a call to `Date.now()` so that rendering
 * stays pure and a test can state the clock instead of chasing it. It is only
 * consulted when the server's own duration is missing or nonsensical: the
 * backend computes `duration_s` against its own clock, which is the right one
 * — the browser's may be minutes out, and an outage whose length depends on
 * whose laptop is open is not a fact.
 *
 * `stale` is the dead-stream flag (SUB-111 / DESIGN.md §6). It does not hide
 * anything: which state we lost an incident in is the most useful thing left
 * on a screen that has stopped updating. It moves every claim out of the
 * present tense, because a word is a louder claim than a hue and CSS cannot
 * reach a word.
 */
export function incidentStory(
  incident: Incident,
  now: number,
  stale = false,
): IncidentStory {
  const state = incidentState(incident);
  const open = state !== "resolved";
  const live = open && !stale;

  const startMoment = formatMoment(incident.startedAt);
  const opening = stale ? "Was down from" : open ? "Down since" : "Down from";
  const began =
    startMoment === null
      ? `${opening.replace(/ (since|from)$/, "")}, start time unknown`
      : `${opening} ${startMoment}`;

  /*
   * Prefer the server's duration; fall back to the clock only when it is
   * absent. A running incident whose `duration_s` the server has not updated
   * since the last check would otherwise appear frozen, which on the one row
   * someone is watching is the difference between "still broken" and "broken
   * and getting worse".
   */
  const measured =
    Number.isFinite(incident.durationS) && incident.durationS > 0
      ? incident.durationS
      : incident.startedAt === null
        ? 0
        : Math.max(
            0,
            Math.floor(
              ((open ? now : (incident.resolvedAt ?? now)) - incident.startedAt) /
                1000,
            ),
          );
  const length = formatDuration(measured);
  const lasted = live
    ? `${length} and counting`
    : open
      ? `${length} when we last heard`
      : length;

  const cause = causeWords(incident.cause);

  const ended =
    incident.resolvedAt === null
      ? null
      : (() => {
          const moment = endingMoment(incident.startedAt, incident.resolvedAt);
          return moment === null ? null : `Recovered at ${moment}`;
        })();

  /*
   * The acknowledgement clause always carries "still down" with it.
   *
   * Written as one string rather than as a flag the caller decorates, because
   * the two halves must not be separable: every rendering of "acknowledged"
   * in this product arrives with the reminder that the service has not come
   * back, and a caller cannot accidentally print only the reassuring half.
   */
  const ackMoment =
    formatClock(incident.ackedAt) ?? formatMoment(incident.ackedAt);
  /*
   * "Muted", not "paused", and it is a claim this build is entitled to make.
   *
   * The design proposal drew ack as a pure human marker with notifications
   * still firing, and left "does Acknowledge silence notifications?" as an
   * open question. The code has since answered it: SUB-81 shipped the
   * escalating repeat ladder and ack genuinely stops it, which the README, the
   * OpenAPI spec and `internal/store/incidents.go` all state. So the sentence
   * says muted rather than hedging — and it says *repeat* alerts, because the
   * first alert has already gone out and the recovery notice will still
   * arrive.
   */
  const stillDown = stale
    ? "was still down, repeat alerts muted"
    : "still down, repeat alerts muted";
  const acked =
    state === "acked"
      ? ackMoment === null
        ? `Acknowledged — ${stillDown}`
        : `Acknowledged at ${ackMoment} — ${stillDown}`
      : null;

  const parts = [
    `${began}, ${lasted}.`,
    cause === null ? null : `${capitalise(cause)}.`,
    ended === null ? null : `${ended}.`,
    acked === null ? null : `${acked}.`,
    // The absence is information too: an open incident nobody has acked is
    // still escalating, and silence about that reads as "handled".
    state === "open"
      ? stale
        ? "Not acknowledged when we lost contact."
        : "Not acknowledged — the repeat alerts are still escalating."
      : null,
  ].filter((part): part is string => part !== null);

  return {
    state,
    badge: stateBadge(state, stale),
    began,
    lasted,
    durationS: measured,
    cause,
    ended,
    acked,
    sentence: parts.join(" "),
  };
}

function capitalise(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/**
 * One step in an incident's timeline.
 *
 * `at` is null for a step that has not happened — the "Not recovered" line the
 * mockup draws with an em dash. It is a real row rather than an omission,
 * because an incident whose timeline simply stops is indistinguishable from
 * one whose timeline failed to render, and the whole point of the panel is
 * that the reader can see the end of the story.
 */
export type TimelineStep = {
  key: string;
  at: number | null;
  what: string;
  /** True for a step that has not happened yet; drawn quieter. */
  pending?: boolean;
};

/**
 * What happened, in order, from the four timestamps the API actually carries.
 *
 * The mockup's timeline also names the channels an alert went to and shows a
 * "flap suppressed" step. Neither is in the incident payload — an incident
 * carries started/confirmed/acked/resolved and nothing about delivery — so
 * they are not drawn. Inventing "Alert sent — slack #ops" from a confirmation
 * timestamp would be the same class of fiction as the sidebar's old "2
 * incidents" badge: plausible, unverifiable, and on a monitoring tool
 * indistinguishable from a fact.
 *
 * What the confirmation step *can* honestly say is that this is the moment a
 * human was told, because that is what confirmation means in the engine.
 */
export function incidentTimeline(
  incident: Incident,
  stale = false,
): TimelineStep[] {
  /*
   * `stale` means the live stream is dead, so every word here describes what
   * we last heard rather than what is true now — the SUB-111 rule, applied to
   * the expanded detail as well as the collapsed line.
   *
   * Without it the row contradicted itself the moment you opened it: the
   * summary said "Was down from 14:02" and the timeline underneath still
   * asserted "Not recovered" and "incident still open" in the present tense,
   * about a stream that stopped reporting minutes ago. The guard on the
   * collapsed line was never the point; not claiming current status is.
   */
  const steps: TimelineStep[] = [
    { key: "started", at: incident.startedAt, what: "First failure observed" },
  ];
  if (incident.confirmed) {
    steps.push({
      key: "confirmed",
      at: incident.confirmedAt,
      what: "Confirmed — this is when a human was told",
    });
  }
  if (incident.acked) {
    steps.push({
      key: "acked",
      at: incident.ackedAt,
      /*
       * The wording SUB-81 made true. Ack really does stop the escalating
       * repeat sequence, so the step says so — and it says "repeat alerts"
       * rather than "alerts", because the first alert already went out and a
       * recovery notice will still arrive.
       */
      what: stale
        ? "Acknowledged — repeat alerts muted, incident was still open"
        : "Acknowledged — repeat alerts muted, incident still open",
    });
  }
  steps.push(
    incident.resolved
      ? { key: "resolved", at: incident.resolvedAt, what: "Recovered" }
      : {
          key: "open",
          at: null,
          what: stale ? "Not recovered when we last heard" : "Not recovered",
          pending: true,
        },
  );
  return steps;
}

/**
 * How far back "recently" reaches when deciding a monitor is bouncing.
 *
 * Matched to the engine's default flap window (`FlapWindow`, 10 minutes) and
 * then widened, because the engine counts *confirmed status changes* while
 * this counts *incident rows*, and an incident that opens and closes inside
 * the window contributes one row to two changes. An hour is the shortest span
 * over which three separate incidents is obviously not coincidence.
 */
export const CHURN_WINDOW_MS = 60 * 60 * 1000;

/** Three separate outages in an hour is a pattern, not bad luck. */
export const CHURN_THRESHOLD = 3;

/**
 * What a monitor that keeps bouncing looks like in this list — or null.
 *
 * **Flapping suppresses the notifications, not the record.** The engine stops
 * forwarding alerts for a monitor that oscillates (see `applyFlapping` in
 * internal/state/engine.go), and it goes on opening and closing incidents the
 * whole time. That is the right behaviour and it has a bad failure mode on
 * screen: the phone goes quiet, and unless the list says why, a quiet phone
 * reads as a service that settled down. It did not; it is bouncing, and the
 * rows underneath are the proof.
 *
 * Derived here rather than read off the wire because the incident payload
 * carries no flapping flag — only the rows, which are the evidence. Counting
 * them is honest about what we actually know: this says "the record shows N
 * separate outages in the last hour", not "the engine has marked this monitor
 * flapping", and the sentence is worded in those terms.
 */
export function describeChurn(
  incidents: readonly Incident[],
  now: number,
): string | null {
  const recent = incidents.filter((incident) => {
    if (incident.startedAt === null) return false;
    /*
     * Bounded at both ends, not just the old one.
     *
     * `now - startedAt <= CHURN_WINDOW_MS` is satisfied by any negative age,
     * so three incidents timestamped in the future counted as "3 separate
     * outages in the last hour" — a flapping notice about a window they are
     * not in. Clock skew between the server and the browser is the ordinary
     * way that happens, and this sentence is loud enough that it should not
     * fire on a machine whose clock is a minute fast.
     */
    const age = now - incident.startedAt;
    return age >= 0 && age <= CHURN_WINDOW_MS;
  });
  if (recent.length < CHURN_THRESHOLD) return null;
  return (
    `${recent.length} separate outages in the last hour. A monitor that ` +
    "oscillates has its repeat alerts suppressed, so the notifications go " +
    "quiet while the incidents below keep being recorded."
  );
}
