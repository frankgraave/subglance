import { IconSort } from "../components/icons";
import { ChoiceControl } from "./ChoiceControl";
import { INVENTORY_SORTS, type InventorySort } from "./inventory";

/**
 * The inventory's Sort button and its panel (SUB-207).
 *
 * Order, not a filter: it changes where rows are, never which are shown, so
 * it stands after Filter. Drawn by `ChoiceControl`, which the incidents
 * screen's History window uses too.
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
  return (
    <ChoiceControl
      label="Sort monitors"
      name="Sort"
      legend="Sort by"
      icon={<IconSort />}
      options={INVENTORY_SORTS}
      value={value}
      onChange={onChange}
      narrow={narrow}
    />
  );
}
