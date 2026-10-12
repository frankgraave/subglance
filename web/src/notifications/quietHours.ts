import type { QuietHours } from "./channels";

/**
 * The quiet-hours form model (SUB-124): what the form starts from, what saving
 * sends, and what is refused before anything is sent. Kept apart from
 * `QuietHoursField` so the component file exports only a component.
 */

export type QuietDraft = {
  enabled: boolean;
  start: string;
  end: string;
  timezone: string;
  /**
   * `hold` or `drop`, or a mode from a newer server, kept verbatim so that
   * saving an untouched form cannot rewrite it.
   */
  during: string;
};

/**
 * A part of the window a refusal can be about, by the server's name for it:
 * the API's quiet-hours refusals carry one of these as their `field`.
 */
export type QuietPart = "start" | "end" | "timezone" | "during";

/**
 * A refused window: what is wrong, and which part it is about. A null part is
 * a refusal about the window as a whole, which stands under the fieldset.
 */
export type QuietRefusal = { message: string; part: QuietPart | null };

/**
 * The part of the window a server field names, or null.
 *
 * The channel form hears a quiet-hours refusal as `quiet_hours.<part>`, the
 * name a configuration import gives the same field, beside `config.<key>` for
 * the channel's own settings.
 */
export function quietPart(field: string | null): QuietPart | null {
  const part = /^quiet_hours\.(start|end|timezone|during)$/.exec(field ?? "")?.[1];
  return (part as QuietPart | undefined) ?? null;
}

/** The form's starting point: the stored window, or hold overnight. */
export function draftFrom(stored: QuietHours | null): QuietDraft {
  if (stored !== null) return { enabled: true, ...stored };
  return {
    enabled: false,
    start: "22:00",
    end: "07:00",
    timezone: browserTimezone(),
    during: "hold",
  };
}

/**
 * What saving should do to the window: undefined when nothing changed, null to
 * remove it, or the window to store.
 *
 * "Nothing changed" is a real answer and not an optimisation. The server
 * releases everything a window is holding whenever the window is replaced, so
 * re-sending an identical window on every channel edit would flush a night's
 * held alerts because somebody renamed the channel at 02:00.
 */
export function quietChange(
  stored: QuietHours | null,
  draft: QuietDraft,
): QuietHours | null | undefined {
  if (!draft.enabled) return stored === null ? undefined : null;
  const next: QuietHours = {
    start: draft.start,
    end: draft.end,
    timezone: draft.timezone.trim(),
    during: draft.during,
  };
  if (
    stored !== null &&
    stored.start === next.start &&
    stored.end === next.end &&
    stored.timezone === next.timezone &&
    stored.during === next.during
  ) {
    return undefined;
  }
  return next;
}

/**
 * The first problem the server would refuse, caught before anything is sent,
 * and the part of the window it is about.
 *
 * Checked here rather than left to the server because the window is a second
 * request after the channel's own: a refusal that arrives after the channel was
 * created would leave a channel without the window its author asked for. The
 * server stays the authority and still validates everything. Each refusal
 * names the part the server would name for it, equal bounds included, so a
 * sentence stands in one place whichever side caught it.
 */
export function quietProblem(draft: QuietDraft): QuietRefusal | null {
  if (!draft.enabled) return null;
  const clock = /^(?:[01][0-9]|2[0-3]):[0-5][0-9]$/;
  for (const part of ["start", "end"] as const) {
    if (!clock.test(draft[part])) {
      return { message: "Quiet hours need a start and an end time.", part };
    }
  }
  if (draft.start === draft.end) {
    return { message: "Quiet hours cannot start and end at the same minute.", part: "end" };
  }
  const zone = draft.timezone.trim();
  if (zone === "" || zone === "Local") {
    return {
      message: "Quiet hours need a named timezone, such as Europe/Amsterdam: the server's own zone is not necessarily yours.",
      part: "timezone",
    };
  }
  try {
    new Intl.DateTimeFormat("en", { timeZone: zone });
  } catch {
    return {
      message: `"${zone}" is not a timezone this browser recognises. Use an IANA name, such as Europe/Amsterdam.`,
      part: "timezone",
    };
  }
  return null;
}

/** The zone this browser is in, or UTC when it cannot say. */
export function browserTimezone(): string {
  try {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    return zone !== undefined && zone !== "" ? zone : "UTC";
  } catch {
    return "UTC";
  }
}

/** Every zone name the browser can list, or none on an older engine. */
export function knownTimezones(): readonly string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return [];
  }
}
