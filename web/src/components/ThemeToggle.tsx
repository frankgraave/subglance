import type { ReactNode } from "react";
import { IconMoon, IconSun, IconThemeAuto } from "./icons";
import { SegmentedControl } from "./SegmentedControl";
import type { ThemePreference } from "../theme/theme";

const OPTIONS: readonly {
  id: ThemePreference;
  label: string;
  icon: ReactNode;
  hint: string;
}[] = [
  { id: "light", label: "Light", icon: <IconSun />, hint: "Light theme" },
  { id: "dark", label: "Dark", icon: <IconMoon />, hint: "Dark theme" },
  {
    id: "system",
    label: "Auto",
    icon: <IconThemeAuto />,
    // Names the mechanism, because "Auto" alone leaves open what it follows —
    // the time of day is the other thing people expect it to mean.
    hint: "Follow the operating system",
  },
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
 *
 * ## Why glyphs and not the words
 *
 * The same argument `CardColumnsSwitcher` makes, plus one this control has and
 * that one does not: theme is the single most universally iconified control on
 * the web. A sun and a crescent are read without being learned, which is the
 * bar an icon has to clear before it is allowed to replace a word.
 *
 * It also buys back the room this bar is short of. "Light Dark Auto" is three
 * words of chrome permanently parked in the corner of every screen, competing
 * with the page for the attention rule 1 wants pointed at the monitors — and
 * on a phone it was the widest thing in a right-aligned group that had already
 * given up its padding. Three 26px squares, at the toolbar's own size.
 *
 * Every option still carries its `label` as the accessible name and a `hint`
 * as the tooltip; the glyph is what is drawn, not what is announced.
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
