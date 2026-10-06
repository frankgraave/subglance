import { useId } from "react";
import { Checkbox, Radio } from "../components/Choice";
import { knownTimezones } from "./quietHours";
import type { QuietDraft } from "./quietHours";
import { FieldError } from "../components/FieldError";

/**
 * The quiet-hours part of the channel form (SUB-124).
 *
 * **Hold is the default and drop has to be chosen.** Dropping an alert is an
 * irreversible decision taken by a settings field at 03:00, so the form opens
 * on hold, lists it first, and says in the drop option's own label what it
 * costs. The server makes the same choice when a client omits the mode.
 *
 * **Native time inputs, not free text.** The mockup draws two text boxes with
 * "23:00" typed in them. A text box accepts "11pm" and "7", which the server
 * refuses, and the refusal arrives after save. `type="time"` can only produce
 * a valid `HH:MM`, is keyboard- and screen-reader-operable in every current
 * browser, and shows the reader their own clock format while sending 24-hour.
 *
 * **The timezone is typed, with the browser's own zone prefilled.** The window
 * is evaluated in this zone, not the server's (the API refuses `Local` for that
 * reason), and the person setting quiet hours almost always means the zone they
 * sleep in. A datalist offers every IANA name the browser knows without a
 * four-hundred-row select, and the server's validation stays the authority.
 *
 * Controlled: the form owns the values, so it can send one request for the
 * channel and one for its window and compare against what was loaded.
 */

export type QuietHoursFieldProps = {
  value: QuietDraft;
  onChange: (next: QuietDraft) => void;
  /** True when the channel already has a window stored on the server. */
  stored: boolean;
  /** A problem with these fields, shown beneath them. */
  error?: string | null;
  /**
   * A sentence about what a window costs on this kind of channel. It joins
   * the help text, which the checkbox is described by, so it is read out at
   * the moment someone is about to turn the window on.
   */
  caveat?: string;
};

export function QuietHoursField({
  value,
  onChange,
  stored,
  error = null,
  caveat,
}: QuietHoursFieldProps) {
  const ids = useId();
  const set = (patch: Partial<QuietDraft>) => onChange({ ...value, ...patch });
  const zones = value.enabled ? knownTimezones() : [];
  const described = error !== null ? `${ids}-help ${ids}-error` : `${ids}-help`;

  return (
    <fieldset className="field nt-quiet">
      <legend className="field-label">Quiet hours</legend>
      <Checkbox
        checked={value.enabled}
        onChange={(event) => set({ enabled: event.target.checked })}
        aria-describedby={`${ids}-help`}
      >
        Hold this channel's alerts during a daily window
      </Checkbox>

      {value.enabled && (
        <>
          <div className="field-grid nt-quiet-grid">
            <div className="field">
              <label className="field-label" htmlFor={`${ids}-start`}>
                From
              </label>
              <input
                id={`${ids}-start`}
                className="input"
                type="time"
                step={60}
                required
                value={value.start}
                onChange={(event) => set({ start: event.target.value })}
                aria-describedby={described}
                {...(error !== null ? { "aria-invalid": true as const } : {})}
              />
            </div>
            <div className="field">
              <label className="field-label" htmlFor={`${ids}-end`}>
                Until
              </label>
              <input
                id={`${ids}-end`}
                className="input"
                type="time"
                step={60}
                required
                value={value.end}
                onChange={(event) => set({ end: event.target.value })}
                aria-describedby={described}
                {...(error !== null ? { "aria-invalid": true as const } : {})}
              />
            </div>
            <div className="field field-wide">
              <label className="field-label" htmlFor={`${ids}-zone`}>
                Timezone
              </label>
              <input
                id={`${ids}-zone`}
                className="input"
                required
                autoComplete="off"
                spellCheck={false}
                list={zones.length > 0 ? `${ids}-zones` : undefined}
                value={value.timezone}
                onChange={(event) => set({ timezone: event.target.value })}
                aria-describedby={described}
                {...(error !== null ? { "aria-invalid": true as const } : {})}
                placeholder="Europe/Amsterdam"
              />
              {zones.length > 0 && (
                <datalist id={`${ids}-zones`}>
                  {zones.map((zone) => (
                    <option key={zone} value={zone}>
                      {zone}
                    </option>
                  ))}
                </datalist>
              )}
            </div>
          </div>

          <fieldset className="nt-quiet-modes">
            <legend className="field-label">During the window</legend>
            <Radio
              name={`${ids}-during`}
              value="hold"
              checked={value.during === "hold"}
              onChange={() => set({ during: "hold" })}
            >
              Hold, then send one digest when the window ends
            </Radio>
            <Radio
              name={`${ids}-during`}
              value="drop"
              checked={value.during === "drop"}
              onChange={() => set({ during: "drop" })}
            >
              Drop — nobody is told, not even afterwards
            </Radio>
          </fieldset>
        </>
      )}

      <p className="field-help" id={`${ids}-help`}>
        {value.enabled
          ? "An end earlier than the start runs past midnight. Recoveries during the window go into the same digest, so a night that fixed itself arrives as one message."
          : "Off: this channel delivers at any hour."}
        {stored &&
          " Changing or removing the window sends whatever it is holding right away."}
        {caveat !== undefined && ` ${caveat}`}
      </p>

      {error !== null && (
        <FieldError id={`${ids}-error`}>
          {error}
        </FieldError>
      )}
    </fieldset>
  );
}
