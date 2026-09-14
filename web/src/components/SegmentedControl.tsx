/**
 * A segmented control: three or four mutually exclusive options, all visible.
 *
 * The component exists because the same control was hand-rolled three times —
 * once in the toolbar and twice in the workbench — and the three copies had
 * already drifted apart in radius, tone and padding. DESIGN.md §7.2 states the
 * pattern, §7.8 now states the component.
 *
 * ## Which variant
 *
 * `variant="view"` (default) is the one that changes *which view* of the data
 * you get: the layout switcher, a density choice. The selection is chrome, so
 * it stays in the neutral scale.
 *
 * `variant="data"` is the one that changes *what data* you are looking at: a
 * time range, a filter over the list. The selection is part of the reading, so
 * it wears the control accent.
 *
 * Picking by what the control does, rather than by how loud it should look, is
 * what stops every segmented control on a screen from shouting at once.
 *
 * ## Why `aria-pressed` and not a radio group
 *
 * Same reason `LayoutSwitcher` chose it before this component existed: these
 * are toggle buttons over one setting, they act on press, and radio semantics
 * would add arrow-key navigation that conflicts with the toolbar around them.
 * The `role="group"` plus its label is what ties them together for a screen
 * reader.
 */
export type SegmentedOption<Id extends string> = {
  id: Id;
  label: string;
  /** Optional `title`, for a difference that is not obvious from the label. */
  hint?: string;
};

export type SegmentedControlProps<Id extends string> = {
  /** Names the group for a screen reader; the buttons alone are just words. */
  label: string;
  options: readonly SegmentedOption<Id>[];
  /** The option drawn as selected. */
  value: Id;
  onChange: (next: Id) => void;
  variant?: "view" | "data";
  className?: string;
};

export function SegmentedControl<Id extends string>({
  label,
  options,
  value,
  onChange,
  variant = "view",
  className,
}: SegmentedControlProps<Id>) {
  return (
    <div
      className={className ? `segmented ${className}` : "segmented"}
      role="group"
      aria-label={label}
      data-variant={variant}
    >
      {options.map((option) => (
        <button
          key={option.id}
          type="button"
          className="segmented-option"
          aria-pressed={value === option.id}
          title={option.hint}
          onClick={() => onChange(option.id)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
