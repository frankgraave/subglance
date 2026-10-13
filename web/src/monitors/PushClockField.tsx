import type { ReactNode } from "react";
import { knownTimezones } from "../notifications/quietHours";

export type PushClockFieldProps = {
  /** The input's id; the help paragraph and the zone list hang off it. */
  id: string;
  /** The IANA zone as typed; empty counts elapsed time. */
  value: string;
  onChange: (value: string) => void;
  /** Set when the server or the form refused this field. */
  invalid?: boolean;
  /** The id of the refusal under the field, added to the description. */
  errorId?: string;
  /** The refusal itself, placed under the help. */
  children?: ReactNode;
};

/**
 * The clock a daily push job runs on: an IANA zone, or empty for elapsed time.
 *
 * **Typed, with the zones as suggestions, and empty by default.** The quiet
 * hours field prefills the browser's zone because a quiet window is always
 * meant in someone's local time. A job is not: one scheduled in UTC on a
 * server, given the browser's Amsterdam here, would be due an hour early the
 * night the clocks go forward and alarm then instead. So nothing is filled in,
 * and the help says which jobs want it.
 *
 * The caller shows it only for an interval of whole days: an hourly job keeps
 * no time of day, and the server refuses a zone beside one.
 */
export function PushClockField({ id, value, onChange, invalid, errorId, children }: PushClockFieldProps) {
  const zones = knownTimezones();
  const help = `${id}-help`;
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>
        Runs on the clock of
      </label>
      <input
        id={id}
        name="push_timezone"
        className="input"
        autoComplete="off"
        spellCheck={false}
        list={zones.length > 0 ? `${id}-zones` : undefined}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder="Europe/Amsterdam"
        aria-invalid={invalid ? true : undefined}
        aria-describedby={errorId ? `${help} ${errorId}` : help}
      />
      <p id={help} className="field-help">
        The job&rsquo;s timezone, if it runs at a fixed local time. It is then
        due at the same local time each day, also the night the clocks change.
        Leave it empty for a job scheduled in UTC.
      </p>
      {children}
      {zones.length > 0 && (
        <datalist id={`${id}-zones`}>
          {zones.map((zone) => (
            <option key={zone} value={zone}>
              {zone}
            </option>
          ))}
        </datalist>
      )}
    </div>
  );
}
