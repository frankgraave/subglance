import { useId, useRef } from "react";

const isNumericZero = (value: string) => value.trim() !== "" && Number(value) === 0;

/** The value stays textual while typing: an empty field must not turn into off. */
export function RepeatAlertField({ value, onChange, error }: {
  value: string; onChange: (value: string) => void; error?: string;
}) {
  const id = useId();
  const modeRef = useRef<HTMLSelectElement>(null);
  const isOff = isNumericZero(value);
  return <div className="add-field repeat-field">
    <label className="add-label" htmlFor={`${id}-mode`}>Repeat alerts</label>
    <select ref={modeRef} id={`${id}-mode`} className="add-input" value={isOff ? "off" : "on"}
      aria-invalid={error ? true : undefined} aria-describedby={`${id}-help${error ? ` ${id}-error` : ""}`}
      onChange={(event) => onChange(event.target.value === "off" ? "0" : "900")}>
      <option value="on">Repeat while unacknowledged</option>
      <option value="off">Do not repeat</option>
    </select>
    {!isOff && <>
      <label className="add-label" htmlFor={`${id}-seconds`}>Repeat alert base (seconds)</label>
      <input id={`${id}-seconds`} className="add-input" inputMode="numeric" data-repeat-input
        value={value} onChange={(event) => {
          const nextValue = event.target.value;
          const turnsOff = isNumericZero(nextValue);
          // Move focus before the seconds field unmounts.
          if (turnsOff && event.currentTarget === document.activeElement) modeRef.current?.focus();
          onChange(turnsOff ? "0" : nextValue);
        }}
        aria-invalid={error ? true : undefined} aria-describedby={`${id}-help${error ? ` ${id}-error` : ""}`} />
    </>}
    <p id={`${id}-help`} className="add-help">Reminders escalate from this base, not at a fixed recurrence. At least 60 seconds keeps reminders from becoming too frequent. Acknowledging stops repeats.</p>
    {error && <p id={`${id}-error`} className="add-field-error" role="alert">{error}</p>}
  </div>;
}
