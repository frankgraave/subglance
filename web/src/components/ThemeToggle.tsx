import { SegmentedControl } from "../components/SegmentedControl";
import type { ThemePreference } from "../theme/theme";

const OPTIONS: readonly { id: ThemePreference; label: string }[] = [
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
  { id: "system", label: "Auto" },
];

/**
 * Three-way theme choice.
 *
 * A two-state toggle cannot express "follow the system", and a dropdown costs
 * a click to even see the current value.
 *
 * This was a hand-rolled fourth copy of the segmented control, written in
 * Tailwind utilities against `bg-surface-2` and `rounded-sm` while the shared
 * component had moved on. Side by side in the topbar the two bars disagreed on
 * radius, padding, border and what "selected" looks like — the reason this now
 * uses `SegmentedControl` like everything else.
 *
 * The cost of the change is real and worth stating: the old markup was a
 * `<fieldset>` of radios, which gave arrow-key navigation for free. The shared
 * control is a group of toggle buttons (see its own note on why), so the keys
 * behave like the rest of the toolbar instead. Consistency of one control
 * across the product beats one control having better keys than its neighbours.
 */
export function ThemeToggle({
  preference,
  onChange,
}: {
  preference: ThemePreference;
  onChange: (next: ThemePreference) => void;
}) {
  return (
    <SegmentedControl
      label="Colour theme"
      options={OPTIONS}
      value={preference}
      onChange={onChange}
    />
  );
}
