import { useId, useState } from "react";
import { StatusChip } from "../components/Chip";
import { IconBellOff } from "../components/icons";
import { Value } from "../components/Value";
import { Led } from "../monitors/Led";
import { formatClock, incidentStory, incidentTimeline, STATE_TONE } from "./story";
import { formatDuration } from "../monitors/detail";
import type { Incident } from "../monitors/detail";

/**
 * One incident: a single-line row that expands in place.
 *
 * The shape is the approved page proposal's, and so is the column order,
 * because that order is the order the questions get asked at 03:00: **is it
 * bad** (the lamp) → **what is it** (the name, with the error under it) →
 * **why** (the failure kind) → **since when** → **for how long** → **is anyone
 * on it** → **can I take it**.
 *
 * ---
 *
 * **It expands inline; it does not open a side panel.**
 *
 * A 320px `.split` column is too narrow to hold an error string, a timeline
 * and a captured response body without wrapping all three into mush, and
 * choosing a row would reflow the entire page at the moment somebody is
 * reading it. Expanding in place keeps the incident where the eye already is,
 * keeps its neighbours in context, and gives the detail the full content
 * width.
 *
 * Several rows can be open at once, which is the point rather than a
 * side-effect: two failures that started a minute apart are usually one event,
 * and comparing them requires seeing both.
 *
 * ---
 *
 * **Acknowledged is not resolved, and this row says so four ways.**
 *
 * The lamp and the status rail state the *service*; the chip in the response column
 * states the *response*. Those are two different questions and they get two
 * different columns — which is what makes them impossible to conflate. On top
 * of that the duration keeps counting, and the sr-only sentence spells the
 * whole thing out. A reader who cannot see colour, or who is listening rather
 * than looking, still gets "still down" from the words alone.
 *
 * ---
 *
 * **What a screen reader hears and what an eye sees are the same text.**
 *
 * The visible row is `aria-hidden` and the whole story is rendered once beside
 * it as `sr-only`, both built from one `incidentStory` call. There is no
 * second string to keep in step, so the divergence DESIGN.md §9.1 exists to
 * prevent is not merely discouraged here — it is unexpressible.
 */

export type IncidentStoryItemProps = {
  incident: Incident;
  /** Now, in unix ms, for durations that are still running. */
  now: number;
  /** The monitor's name. Absent on a monitor's own page, where it is the title. */
  subject?: string;
  /** Acknowledges this incident. Absent means the control is not offered. */
  onAck?: (id: string) => void;
  /** True while this incident's ack request is in flight. */
  acking?: boolean;
  /** True when the live stream is dead; moves every claim out of the present. */
  stale?: boolean;
  /** Renders the quieter history treatment: no status rail, dimmer name. */
  past?: boolean;
};

export function IncidentStoryItem({
  incident,
  now,
  subject,
  onAck,
  acking = false,
  stale = false,
  past = false,
}: IncidentStoryItemProps) {
  const story = incidentStory(incident, now, stale);
  const [open, setOpen] = useState(false);
  const detailId = useId();

  /*
   * The button appears on open *and* on acked-but-unresolved incidents, and is
   * absent on resolved ones — acking history would be a control that does
   * nothing. It is disabled rather than removed once acked, because a row
   * whose control vanishes gives the reader no evidence their click landed.
   */
  const ackable = story.state !== "resolved" && onAck !== undefined;
  const timeline = incidentTimeline(incident, stale);

  /*
   * The control's words, computed once and used three ways: as the accessible
   * name, as the pointer's title, and as the visible label in the expanded
   * footer. One string, so the glyph the eye sees on a collapsed row and the
   * name a screen reader hears cannot come to describe different acts.
   */
  const ackWord = incident.acked
    ? "Repeat alerts muted"
    : acking
      ? "Muting…"
      : "Mute repeat alerts";

  /*
   * Which incident this mutes. An icon-only control named "Mute" is ambiguous
   * the moment a second row appears, and this list is never one row long at
   * the time anybody reads it.
   *
   * The monitor's name when there is one — that is what distinguishes rows on
   * the incidents screen. On a monitor's own page there is no subject, because
   * the monitor is the page title, and the rows there differ by *when*; so the
   * fallback is the start time rather than a repetition of the title.
   */
  const which =
    subject ?? `the incident from ${formatClock(incident.startedAt) ?? story.began}`;

  return (
    <li
      className={past ? "inc-row inc-row--past" : "inc-row"}
      data-state={story.state}
      data-open={open ? "true" : "false"}
    >
      {/*
       * The collapsed line is a button, and the whole line is the target.
       *
       * A row you can click needs to be a control a keyboard can reach, and
       * `aria-expanded` is what tells a screen reader this is a disclosure
       * rather than a link to somewhere. The mockup made the row itself
       * `tabindex="0"` with a keydown handler; a real `<button>` gets Enter,
       * Space, focus styling and the role for free, and cannot forget one.
       */}
      <button
        type="button"
        className="inc-line"
        aria-expanded={open}
        aria-controls={detailId}
        onClick={() => setOpen((was) => !was)}
      >
        {/*
         * The sentence lives *inside* the button, because it is the button's
         * accessible name.
         *
         * It used to sit outside as a sibling, with the button's contents
         * `aria-hidden` to stop the story being heard twice. That silenced the
         * duplicate and the name together: `aria-hidden` removes content from
         * the accessible name computation, so the disclosure announced itself
         * as an unnamed button and the reader had to guess what expanding it
         * would reveal. Inside, one string does both jobs — and it is still
         * the same `story.sentence` the eye is shown below, so the two cannot
         * drift into describing different incidents.
         */}
        <span className="sr-only">
          {subject === undefined ? null : <>{subject}: </>}
          {story.sentence}
        </span>
        <span className="inc-line-inner" aria-hidden="true">
          {/*
           * The lamp states the service. `labelled={false}` because the
           * sr-only sentence already says it in words, and a lamp that
           * repeats it would have a screen reader say "down" twice.
           */}
          <Led
            status={story.state === "resolved" ? "up" : "down"}
            labelled={false}
            className="inc-led"
          />
          <span className="inc-main">
            <span className="inc-name">
              {subject ?? story.began}
            </span>
            {/* The error, one line, clipped. The full string is in the detail
                below, where it has room to wrap. */}
            <span className="inc-sub">
              {incident.lastError ?? story.cause ?? story.began}
            </span>
          </span>
          {/* The failure kind as a word, so the row still reads in greyscale
              and a colour-blind reader loses nothing (DESIGN.md §2.3). */}
          <span className="inc-col inc-col-kind">
            {story.cause === null ? null : (
              <span className="chip chip--state">{story.cause}</span>
            )}
          </span>
          <Value className="inc-col inc-col-time">
            {formatClock(incident.startedAt) ?? "—"}
          </Value>
          <Value className="inc-col inc-col-dur">
            {formatDuration(story.durationS)}
          </Value>
          {/*
           * The response column: is anybody on it. Separate from the lamp on
           * purpose — the service's state and the response's state are two
           * questions, and one combined word is how "acked" comes to read as
           * "fixed".
           */}
          <span className="inc-col inc-col-ack">
            <StatusChip status={STATE_TONE[story.state]}>
              {story.badge}
            </StatusChip>
          </span>
        </span>
      </button>

      {/*
       * The action sits outside the disclosure button.
       *
       * A button inside a button is invalid HTML and browsers recover from it
       * by unnesting, which silently changes the layout. The mockup solved
       * this with `stopPropagation` on a click handler; the markup does not
       * have to create the problem in the first place.
       */}
      {ackable ? (
        <div className="inc-act">
          {/*
           * One click, where the incident is — not behind a detail screen.
           *
           * Ack's job is to stop an escalating repeat sequence, and a control
           * that takes three navigations to reach is one nobody uses at 03:00,
           * which leaves the repeats running.
           *
           * **The label names the consequence, and the consequence is real.**
           * The proposal asked whether ack silences notifications and drew it
           * as a pure human marker. SUB-81 has since answered the question in
           * code: ack stops the 15min/1h/4h/16h/daily ladder. So the label is
           * "Mute repeat alerts", which states the promise the button actually
           * keeps — and deliberately avoids the word "Acknowledge" standing
           * alone, which reads as "mark this done" and is the one label that
           * could make a still-broken service look handled. The response
           * column beside it keeps saying "still down" afterwards.
           *
           * ---
           *
           * **A glyph while the row is closed, the same words once it opens.**
           *
           * The word cost a whole strip. `.inc-act` was a block of its own
           * under the line, so a collapsed row measured 108px to carry a 60px
           * line — 46px per row spent on one control, and a screen of open
           * incidents is mostly repeated buttons. On the line there is no room
           * for a 113px label beside four columns, and there is exactly room
           * for the 26px square this product already uses for a per-row action
           * (SUB-134 on monitors, SUB-137 on channels). This is the third
           * screen to agree with that pattern rather than the first to invent
           * something.
           *
           * `IconBellOff` and not a new shape, because it is the same act: the
           * channels screen draws that bell for "leave every check running and
           * stop telling anyone", which is precisely what acking does to the
           * repeat ladder. A different glyph for the same promise would be the
           * reader's problem, not the drawer's.
           *
           * The word comes back in the expanded footer, where the tray's fill
           * gives it room. That is deliberate on both counts: opening a row is
           * how a reader who does not recognise the bell learns what it does,
           * and a control that changes shape between the two states is one
           * more thing making them read as different states rather than as the
           * same row at two heights.
           *
           * `aria-label` is set in *both* forms and names the incident, so the
           * name never depends on which state the row is in and a list of five
           * unacked incidents does not announce five buttons called "Mute".
           * Screen readers do not agree on what an unnamed inline `<svg>` is
           * (AGENTS.md), so the name is in the markup.
           */}
          <button
            type="button"
            className={open ? "add-button inv-act" : "inv-act inv-act--icon"}
            onClick={() => onAck?.(incident.id)}
            disabled={acking || incident.acked}
            aria-busy={acking}
            aria-label={`${ackWord} for ${which}`}
            title={`${ackWord} for ${which}. Stops the escalating repeat notifications. The incident stays open until the service recovers.`}
          >
            {open ? ackWord : <IconBellOff />}
          </button>
        </div>
      ) : null}

      {/*
       * The detail. Rendered only when open — an off-screen copy would keep
       * its text readable to a screen reader and its controls focusable, which
       * is the usual way this pattern is got wrong.
       */}
      {open ? (
        <div className="inc-detail" id={detailId}>
          <div className="inc-detail-grid">
            <div className="inc-detail-col">
              <p className="inc-label">Timeline</p>
              <ol className="inc-tl">
                {timeline.map((step) => (
                  <li key={step.key} className="inc-tl-step">
                    <Value className="inc-tl-at">
                      {step.at === null ? "—" : (formatClock(step.at) ?? "—")}
                    </Value>
                    <span
                      className={step.pending ? "inc-tl-what--pending" : undefined}
                    >
                      {step.what}
                    </span>
                  </li>
                ))}
              </ol>
            </div>
            <div className="inc-detail-col">
              <p className="inc-label">Last error</p>
              {incident.lastError ? (
                <p className="inc-snap inc-snap--err">{incident.lastError}</p>
              ) : (
                /*
                 * Said rather than left blank. An empty slot where an error
                 * belongs reads as a rendering fault; "not recorded" is a
                 * statement about the data, which is what it actually is.
                 */
                <p className="inc-helper">
                  No error text was recorded for this failure.
                </p>
              )}
              {/*
               * The mockup also shows a captured response body here. The
               * incident payload does not carry one — there is no field for it
               * on `incidentResponse` — so it is not drawn. A panel headed
               * "Response snapshot" over invented content would be worse than
               * its absence on a tool whose whole value is that what it shows
               * is true.
               */}
            </div>
          </div>
        </div>
      ) : null}
    </li>
  );
}
