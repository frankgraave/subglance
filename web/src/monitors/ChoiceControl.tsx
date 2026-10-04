import { useId } from "react";
import type { ReactNode } from "react";
import { Radio } from "../components/Choice";
import { Popover } from "../components/Popover";

/**
 * A header button that names one current choice on its face and opens a
 * single radio list: the inventory's Sort and the incidents screen's History
 * window (SUB-207).
 *
 * The choice is made at the head of the list, like the dashboard's View: the
 * button says what is chosen, so it is readable without opening it, and its
 * accessible name says the same ("Sort: Name", "History: 30 days"). On a
 * phone it is a square glyph button and the panel a sheet from the bottom
 * edge, as the dashboard's Filter and View are.
 */
export type ChoiceOption<V extends string | number> = { value: V; label: string };

export function ChoiceControl<V extends string | number>({
  label,
  name,
  legend,
  icon,
  options,
  value,
  onChange,
  narrow,
}: {
  /** The panel's accessible name: "Sort monitors". */
  label: string;
  /** What the choice is, before the current value in the button's name: "Sort". */
  name: string;
  /** The radio list's legend: "Sort by". */
  legend: string;
  /** The glyph on the button, which is all of its face on a phone. */
  icon: ReactNode;
  options: readonly ChoiceOption<V>[];
  value: V;
  onChange: (next: V) => void;
  narrow: boolean;
}) {
  const group = useId();
  const current = options.find((entry) => entry.value === value)?.label ?? String(value);
  return (
    <Popover
      label={label}
      triggerLabel={`${name}: ${current}`}
      triggerClassName="button button--compact mon-head-button"
      className={narrow ? "mon-panel mon-sheet" : "mon-panel"}
      trigger={
        <>
          {narrow ? null : <span>{current}</span>}
          {icon}
        </>
      }
    >
      {(close) => (
        <>
          <fieldset className="mon-values">
            <legend className="mon-values-key">{legend}</legend>
            {options.map((entry) => (
              <Radio
                key={entry.value}
                name={group}
                checked={entry.value === value}
                onChange={() => onChange(entry.value)}
              >
                {entry.label}
              </Radio>
            ))}
          </fieldset>
          {narrow ? (
            <button
              type="button"
              className="button button--primary mon-sheet-done"
              onClick={close}
            >
              Done
            </button>
          ) : null}
        </>
      )}
    </Popover>
  );
}
