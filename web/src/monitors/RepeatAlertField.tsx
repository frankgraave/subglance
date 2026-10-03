import { useId, useRef } from "react";
import { DurationField } from "./DurationField";
import { SECONDS_TO_HOURS } from "./duration";

const isNumericZero = (value: string) => value.trim() !== "" && Number(value) === 0;

/**
 * Whether to repeat an alert nobody has acknowledged, and how long to wait
 * before the first repeat.
 *
 * The wait is a number and a unit rather than a count of seconds: the API's
 * 900 is shown as 15 minutes, which is how the rest of the product writes it.
 * Later repeats are not this gap again but four times the one before
 * (`internal/state/reminder.go`), so the label names the first one only.
 *
 * The value stays textual while typing: an empty field must not turn into off.
 */
export function RepeatAlertField({ value, onChange, error }: {
  value: string; onChange: (value: string) => void; error?: string;
}) {
  const id = useId();
  const modeRef = useRef<HTMLSelectElement>(null);
  const isOff = isNumericZero(value);
  const described = `${id}-help${error ? ` ${id}-error` : ""}`;
  return <div className="field repeat-field">
    <label className="field-label" htmlFor={`${id}-mode`}>Repeat alerts</label>
    <select ref={modeRef} id={`${id}-mode`} className="input" value={isOff ? "off" : "on"}
      aria-invalid={error ? true : undefined} aria-describedby={described}
      onChange={(event) => onChange(event.target.value === "off" ? "0" : "900")}>
      <option value="on">Repeat while unacknowledged</option>
      <option value="off">Do not repeat</option>
    </select>
    {!isOff && <>
      <label className="field-label" htmlFor={`${id}-seconds`}>First repeat after</label>
      <DurationField id={`${id}-seconds`} value={value} units={SECONDS_TO_HOURS} label="First repeat after"
        inputProps={{ "data-repeat-input": "", "aria-invalid": error ? true : undefined, "aria-describedby": described }}
        onChange={(next) => {
          const turnsOff = isNumericZero(next);
          // Move focus before the amount field unmounts.
          if (turnsOff && document.activeElement?.id === `${id}-seconds`) modeRef.current?.focus();
          onChange(turnsOff ? "0" : next);
        }} />
    </>}
    <p id={`${id}-help`} className="field-help">Each later repeat waits four times as long as the one before, up to once a day. At least 1 minute keeps reminders from becoming too frequent. Acknowledging stops repeats.</p>
    {error && <p id={`${id}-error`} className="field-error" role="alert">{error}</p>}
  </div>;
}
