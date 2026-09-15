/**
 * A segmented control: three or four mutually exclusive options, all visible.
 *
 * The component exists because the same control was hand-rolled three times —
 * once in the toolbar and twice in the workbench — and the three copies had
 * already drifted apart in radius, tone and padding. DESIGN.md §7.2 states the
 * pattern, §7.8 now states the component.
 *
 * ## One selected state
 *
 * There used to be a `variant` prop here: neutral for a control that changes
 * the *view*, accent for one that changes *what data* you see. It is gone.
 * Measured against the reference, the distinction does not exist — its range
 * selector is a view control by our own definition and it fills the active
 * segment with the accent. The split produced a selected segment drawn as a
 * grey box, which reads as disabled rather than as chosen, and it made two
 * controls side by side in the same toolbar disagree about what "selected"
 * looks like.
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
  className?: string;
};

export function SegmentedControl<Id extends string>({
  label,
  options,
  value,
  onChange,
  className,
}: SegmentedControlProps<Id>) {
  return (
    <div
      className={className ? `segmented ${className}` : "segmented"}
      role="group"
      aria-label={label}
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
