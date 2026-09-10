import type { ThemePreference } from "../theme/theme";

const OPTIONS: readonly { value: ThemePreference; label: string }[] = [
  { value: "light", label: "Light" },
  { value: "dark", label: "Dark" },
  { value: "system", label: "Auto" },
];

/**
 * Three-way segmented control.
 *
 * A two-state toggle cannot express "follow the system", and a dropdown costs
 * a click to even see the current value. Rendered as radios so arrow keys and
 * screen readers work without any custom key handling.
 */
export function ThemeToggle({
  preference,
  onChange,
}: {
  preference: ThemePreference;
  onChange: (next: ThemePreference) => void;
}) {
  return (
    <fieldset className="flex items-center gap-1 rounded-sm border border-border bg-surface-2 p-0.5">
      <legend className="sr-only">Colour theme</legend>
      {OPTIONS.map((option) => {
        const active = option.value === preference;
        return (
          <label
            key={option.value}
            className={[
              "cursor-pointer rounded-sm px-2.5 py-1 text-[12.5px] transition-colors",
              active ? "bg-surface-hi text-ink" : "text-ink-3 hover:text-ink-2",
            ].join(" ")}
          >
            <input
              type="radio"
              name="theme"
              value={option.value}
              checked={active}
              onChange={() => onChange(option.value)}
              className="sr-only"
            />
            {option.label}
          </label>
        );
      })}
    </fieldset>
  );
}
