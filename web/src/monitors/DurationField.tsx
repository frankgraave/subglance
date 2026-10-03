import { useState } from "react";
import type { InputHTMLAttributes } from "react";
import { amountIn, sameWire, toWire, unitFor, unitName } from "./duration";
import type { DurationUnit } from "./duration";
import { Select } from "../components/Select";

export type DurationFieldProps = {
  /** The number box's id, which the caller's `<label htmlFor>` points at. */
  id: string;
  /** The wire value as text: seconds, minutes or days, as `units` says. */
  value: string;
  /** Receives the wire value as text, never a rounded or guessed number. */
  onChange: (value: string) => void;
  /** Smallest first. One unit draws a fixed addon; more draw a unit picker. */
  units: readonly DurationUnit[];
  /** The visible label's words. The unit picker's name is built from it. */
  label: string;
  /** Spread onto the number box: name, aria-invalid, aria-describedby, data hooks. */
  inputProps?: InputHTMLAttributes<HTMLInputElement> & { [hook: `data-${string}`]: string };
};

/**
 * A duration as a number and a unit (DESIGN.md §7.2).
 *
 * **A picker beside the box, not a parser inside it.** A box that accepts
 * "1h30m" is quicker for someone who knows it does, and a guess for everyone
 * else; a unit picker shows its whole vocabulary. It is a select rather than
 * the segmented control §7.2 asks for with three or four options, because the
 * unit qualifies the number rather than being a choice of its own, and four
 * segments beside a box do not fit the 12rem a field gets in the forms' grid.
 *
 * **Changing the unit keeps the number.** Typing 2 and then choosing hours
 * means two hours; converting 2 minutes into 0.0333 hours would undo the
 * typing it followed.
 *
 * **The typed text is this field's, the value is the form's.** The box keeps
 * what was typed ("1." on the way to "1.5") and reports the wire value on
 * every keystroke. A value that arrives from outside, a reload after a
 * conflict say, replaces both the number and the unit.
 */
export function DurationField({ id, value, onChange, units, label, inputProps }: DurationFieldProps) {
  const [shown, setShown] = useState(() => {
    const unit = unitFor(value, units);
    return { value, unit: unit.id, amount: amountIn(value, unit) };
  });
  if (!sameWire(value, shown.value)) {
    const unit = unitFor(value, units);
    setShown({ value, unit: unit.id, amount: amountIn(value, unit) });
  }
  const unit = units.find((u) => u.id === shown.unit) ?? units[0];
  const report = (amount: string, next: DurationUnit) => {
    const wire = toWire(amount, next);
    setShown({ value: wire, unit: next.id, amount });
    onChange(wire);
  };
  // A single unit is a whole number of it; with a picker, 1.5 hours is fair.
  const box = <input id={id} className="input" inputMode={units.length === 1 ? "numeric" : "decimal"} autoComplete="off" {...inputProps}
    value={shown.amount} onChange={(event) => report(event.target.value, unit)} />;
  if (units.length === 1) {
    return <div className="add-addon">{box}<span className="add-unit" aria-hidden="true">{unit.short}</span></div>;
  }
  return <div className="mon-duration">
    {box}
    <Select className="input input--fit" aria-label={`${label}: unit`} value={unit.id}
      onChange={(event) => report(shown.amount, units.find((u) => u.id === event.target.value) ?? unit)}>
      {units.map((u) => <option key={u.id} value={u.id}>{unitName(u, shown.amount)}</option>)}
    </Select>
  </div>;
}
