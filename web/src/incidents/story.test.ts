import { describe, expect, it } from "vitest";
import {
  CHURN_THRESHOLD,
  causeWords,
  describeChurn,
  incidentState,
  incidentStory,
  incidentTimeline,
  stateBadge,
  STATE_BADGE,
  STATE_BADGE_LAST_KNOWN,
  STATE_TONE,
} from "./story";
import type { Incident } from "../monitors/detail";

/**
 * SUB-34, the half that is words rather than markup.
 *
 * Two rules are held down here, and they are the two the ticket is actually
 * about:
 *
 * 1. **Acknowledging does not close an incident.** `acked` is a state of the
 *    response, not of the service, and every sentence that mentions it also
 *    says the service is still down. A test that only checked "the word
 *    acknowledged appears" would pass against the very bug this file exists to
 *    prevent, so the assertions are about what the sentence *claims*.
 *
 * 2. **A cause is a sentence, not a key.** "dns" is a database value; "DNS
 *    failure" is what the ticket asked for.
 *
 * Pure strings, no renderer: the wording is the product decision, and the
 * decision deserves a test that cannot be broken by a layout change.
 */

/** 15 Nov 2023, 14:03 UTC-ish; only the arithmetic between them matters. */
const T0 = 1_700_000_000_000;

const incident = (over: Partial<Incident> = {}): Incident => ({
  id: "1",
  monitorId: "7",
  startedAt: T0,
  confirmedAt: T0 + 60_000,
  resolvedAt: null,
  ackedAt: null,
  confirmed: true,
  resolved: false,
  acked: false,
  durationS: 720,
  ...over,
});

describe("the three states, which are never two", () => {
  it("calls an unresolved, unacknowledged incident open", () => {
    expect(incidentState(incident())).toBe("open");
  });

  it("calls an acknowledged but unresolved incident acked, not resolved", () => {
    // The whole ticket in one assertion. Acking stops the repeat alerts; the
    // service is still down, and the state has to say so.
    expect(
      incidentState(incident({ acked: true, ackedAt: T0 + 420_000 })),
    ).toBe("acked");
  });

  it("calls a resolved incident resolved even when it was acked first", () => {
    expect(
      incidentState(
        incident({ acked: true, resolved: true, resolvedAt: T0 + 720_000 }),
      ),
    ).toBe("resolved");
  });
});

describe("the badge word says the state, never only the colour", () => {
  it("never labels an acknowledged incident as though it were over", () => {
    /*
     * "Acknowledged" on its own is the label that makes a still-broken
     * service look handled, which is the exact failure mode the ticket
     * names. The badge has to carry both halves even though its column is
     * narrow — a badge is not allowed to be shorter than the truth.
     */
    expect(STATE_BADGE.acked).toMatch(/still down/i);
    expect(STATE_BADGE.acked).not.toBe("Acknowledged");
  });

  it("gives every state a distinct word, so colour is never the only signal", () => {
    const words = Object.values(STATE_BADGE);
    expect(new Set(words).size).toBe(words.length);
    for (const word of words) expect(word.trim()).not.toBe("");
  });

  it("refuses the healthy colour for an acknowledged incident", () => {
    // `up` here would say "fixed" in the channel that reads fastest, and it
    // would say it louder than the word beside it.
    expect(STATE_TONE.acked).toBe("warn");
    expect(STATE_TONE.open).toBe("down");
    expect(STATE_TONE.acked).not.toBe("up");
  });

  it("moves the badge out of the present tense on a dead stream (SUB-111)", () => {
    // Once the stream is dead no view asserts anything about now, and a
    // stylesheet cannot reach a word.
    expect(stateBadge("open", true)).toBe("Was unacked");
    expect(stateBadge("open", false)).toBe("Unacked");
    expect(stateBadge("acked", true)).toMatch(/was still down/i);
    for (const phrase of Object.values(STATE_BADGE_LAST_KNOWN)) {
      expect(phrase, `"${phrase}" reads as a claim about now`).not.toMatch(
        /\bis still down\b/i,
      );
    }
  });
});

describe("the cause, in words a person would use", () => {
  it("translates the checker's kinds into sentences", () => {
    expect(causeWords("dns")).toBe("DNS failure");
    expect(causeWords("connection")).toBe("connection refused");
    expect(causeWords("push_overdue")).toBe("no report received");
  });

  it("passes an unknown kind through rather than losing it", () => {
    // A newer server adding a kind must not make its incidents read as
    // causeless: an unfamiliar word is more information than no word.
    expect(causeWords("quantum_flux")).toBe("quantum_flux");
  });

  it("treats an absent cause as absent, not as 'unknown'", () => {
    expect(causeWords(undefined)).toBeNull();
    expect(causeWords("")).toBeNull();
  });
});

describe("the sentence a reader is given", () => {
  it("tells the whole story of a resolved incident", () => {
    // The shape the ticket asked for, in order: when, how long, why, and when
    // it came back.
    const story = incidentStory(
      incident({
        resolved: true,
        resolvedAt: T0 + 720_000,
        durationS: 720,
        cause: "dns",
      }),
      T0 + 900_000,
    );
    expect(story.sentence).toMatch(/Down from/);
    expect(story.sentence).toContain("12 min");
    expect(story.sentence).toContain("DNS failure");
    expect(story.sentence).toMatch(/Recovered at/);
  });

  it("keeps an open incident in the present tense and counting", () => {
    const story = incidentStory(incident({ durationS: 720 }), T0 + 720_000);
    expect(story.sentence).toContain("Down since");
    expect(story.sentence).toContain("12 min and counting");
    expect(story.sentence).not.toContain("Recovered");
  });

  it("says an acknowledged incident is STILL DOWN, in the same sentence", () => {
    /*
     * The assertion this whole file is built around.
     *
     * Not "the word acknowledged is present" — that passes against the bug.
     * The sentence has to make the ongoing outage unmissable in the same
     * breath as the acknowledgement, and it must not claim a recovery.
     */
    const story = incidentStory(
      incident({ acked: true, ackedAt: T0 + 420_000, durationS: 720 }),
      T0 + 720_000,
    );
    expect(story.sentence).toMatch(/Acknowledged/);
    expect(story.sentence).toMatch(/still down/i);
    expect(story.sentence).toContain("and counting");
    expect(story.sentence).not.toMatch(/Recovered|Resolved/i);
  });

  it("cannot print the reassuring half of the ack clause on its own", () => {
    // `acked` is one string, not a flag a caller decorates: there is no way
    // for a component to render "Acknowledged at 14:10" without the rest.
    const story = incidentStory(
      incident({ acked: true, ackedAt: T0 + 420_000 }),
      T0 + 720_000,
    );
    expect(story.acked).not.toBeNull();
    expect(story.acked!).toMatch(/still down/i);
  });

  it("does not invent delivery status for an open incident during maintenance", () => {
    // Incident state alone cannot prove a delivery: maintenance can mute it.
    const story = incidentStory(incident(), T0 + 720_000);
    expect(story.sentence).toMatch(/Not acknowledged/i);
    expect(story.sentence).not.toMatch(/escalating|alerts are/i);
  });

  it("says ack mutes the repeats, because SUB-81 made that true", () => {
    /*
     * The design proposal drew ack as a pure human marker and left "does it
     * silence notifications?" open. The code answered it: the escalating
     * repeat ladder is built, and ack stops it. So the sentence states the
     * promise rather than hedging — and says *repeat* alerts, because the
     * first alert already went out and the recovery notice still will.
     */
    const story = incidentStory(
      incident({ acked: true, ackedAt: T0 + 420_000 }),
      T0 + 720_000,
    );
    expect(story.acked!).toMatch(/muted/i);
    expect(story.acked!).toMatch(/repeat/i);
  });

  it("never says an acknowledged incident recovered", () => {
    const story = incidentStory(
      incident({ acked: true, ackedAt: T0 + 60_000 }),
      T0 + 720_000,
    );
    expect(story.ended).toBeNull();
  });

  it("stops asserting the present tense when the stream is dead", () => {
    const story = incidentStory(incident({ durationS: 720 }), T0 + 720_000, true);
    expect(story.sentence).not.toContain("Down since");
    expect(story.sentence).not.toContain("and counting");
    expect(story.sentence).toContain("Was down from");
    expect(story.sentence).toContain("when we last heard");
  });

  it("survives a timestamp it cannot parse rather than printing Invalid Date", () => {
    const story = incidentStory(incident({ startedAt: null }), T0);
    expect(story.sentence).toContain("start time unknown");
    expect(story.sentence).not.toMatch(/Invalid Date|NaN/);
  });

  it("prefers the server's duration to the browser's clock", () => {
    // The server computed `duration_s` against its own clock, which is the
    // right one: a browser minutes out of step must not decide how long an
    // outage has lasted.
    const story = incidentStory(
      incident({ durationS: 3600 }),
      T0 + 60_000, // a clock that would say "1 min"
    );
    expect(story.lasted).toContain("1 h");
  });
});

describe("the timeline says only what the API carries", () => {
  it("walks from first failure to the ending", () => {
    const steps = incidentTimeline(
      incident({ resolved: true, resolvedAt: T0 + 720_000 }),
    );
    expect(steps.map((s) => s.key)).toEqual([
      "started",
      "confirmed",
      "resolved",
    ]);
  });

  it("ends on an explicit 'not recovered' row rather than stopping", () => {
    // A timeline that simply stops is indistinguishable from one that failed
    // to render, and the end of the story is the thing the panel is for.
    const steps = incidentTimeline(incident());
    const last = steps[steps.length - 1];
    expect(last.key).toBe("open");
    expect(last.at).toBeNull();
    expect(last.pending).toBe(true);
  });

  it("puts the ack between confirmation and the ending, still open", () => {
    const steps = incidentTimeline(
      incident({ acked: true, ackedAt: T0 + 420_000 }),
    );
    expect(steps.map((s) => s.key)).toEqual([
      "started",
      "confirmed",
      "acked",
      "open",
    ]);
    expect(steps[2].what).toMatch(/still open/i);
    expect(steps[2].what).toMatch(/muted/i);
  });

  it("invents no alert-delivery steps, because the payload has none", () => {
    /*
     * The mockup's timeline names channels ("Alert sent — slack #ops") and
     * shows flap suppression. An incident carries four timestamps and nothing
     * about delivery, so drawing either would be fiction of exactly the kind
     * the sidebar's old "2 incidents" badge was: plausible, unverifiable, and
     * on a monitoring tool indistinguishable from a fact.
     */
    const text = incidentTimeline(incident({ acked: true, ackedAt: T0 }))
      .map((s) => s.what)
      .join(" ");
    expect(text).not.toMatch(/slack|pagerduty|email|webhook/i);
    expect(text).not.toMatch(/alert sent/i);
  });
});

describe("flapping suppresses the alerts, not the record", () => {
  const bouncing = (n: number): Incident[] =>
    Array.from({ length: n }, (_, i) =>
      incident({
        id: String(i),
        startedAt: T0 - i * 5 * 60_000,
        resolved: true,
        resolvedAt: T0 - i * 5 * 60_000 + 60_000,
      }),
    );

  it("says nothing when a monitor has had one bad moment", () => {
    expect(describeChurn(bouncing(1), T0)).toBeNull();
    expect(describeChurn(bouncing(CHURN_THRESHOLD - 1), T0)).toBeNull();
  });

  it("names the pattern, and says why the phone went quiet", () => {
    /*
     * The second thing the ticket is explicit about. A flapping monitor still
     * gets incident rows; only the notifications stop. If the screen does not
     * say so, a silent phone above a list of rows reads as a service that
     * settled down — when it is in fact bouncing.
     */
    const note = describeChurn(bouncing(4), T0)!;
    expect(note).toContain("4 separate outages");
    expect(note).toMatch(/suppress/i);
    expect(note).toMatch(/recorded/i);
  });

  it("does not count outages from outside the window", () => {
    const old = bouncing(4).map((i) => ({
      ...i,
      startedAt: (i.startedAt ?? 0) - 6 * 60 * 60_000,
    }));
    expect(describeChurn(old, T0)).toBeNull();
  });

  it("ignores rows it cannot date rather than counting them as recent", () => {
    const undated = bouncing(4).map((i) => ({ ...i, startedAt: null }));
    expect(describeChurn(undated, T0)).toBeNull();
  });

  it("does not count incidents stamped in the future", () => {
    /*
     * The window was bounded at one end only: a negative age satisfies
     * `age <= CHURN_WINDOW_MS`, so incidents timestamped ahead of the browser
     * clock counted as "in the last hour". Clock skew between the server and
     * the reader's machine is the ordinary way that happens, and a flapping
     * notice is loud enough that it must not fire on a clock a minute fast.
     */
    const ahead = bouncing(4).map((i) => ({
      ...i,
      startedAt: (i.startedAt ?? 0) + 6 * 60 * 60_000,
    }));
    expect(describeChurn(ahead, T0)).toBeNull();
  });
});
