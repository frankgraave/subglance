import { useId } from "react";
import { Radio } from "../components/Choice";
import { IconSort } from "../components/icons";
import { Popover } from "../components/Popover";
import { INVENTORY_SORTS, type InventorySort } from "./inventory";

/**
 * The inventory's Sort button and its panel (SUB-207).
 *
 * The order is a choice made at the head of the list, like the dashboard's
 * View: the button says the current order, so it is readable without
 * opening it, and the panel is one radio list. Order, not a filter: it
 * changes where rows are, never which are shown, so it stands after Filter.
 * On a phone it is a square glyph button and the panel a sheet from the
 * bottom edge, as the dashboard's are.
 */
export function SortControl({
  value,
  onChange,
  narrow,
}: {
  value: InventorySort;
  onChange: (next: InventorySort) => void;
  narrow: boolean;
}) {
  const name = useId();
  const current = INVENTORY_SORTS.find((entry) => entry.value === value)?.label ?? value;
  return (
    <Popover
      label="Sort monitors"
      triggerLabel={`Sort: ${current}`}
      triggerClassName="button button--compact mon-head-button"
      className={narrow ? "mon-panel mon-sheet" : "mon-panel"}
      trigger={
        <>
          {narrow ? null : <span>{current}</span>}
          <IconSort />
        </>
      }
    >
      {(close) => (
        <>
          <fieldset className="mon-values">
            <legend className="mon-values-key">Sort by</legend>
            {INVENTORY_SORTS.map((entry) => (
              <Radio
                key={entry.value}
                name={name}
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
