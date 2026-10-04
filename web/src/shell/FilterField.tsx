import { SearchIcon } from "./icons";

/**
 * The field that narrows a list to the rows whose words match.
 *
 * Every list that can be filtered by typing draws this one field, wherever it
 * stands: in the header of the card it filters (the channels), above the
 * index it filters (the settings sections), or, until those screens move
 * their controls into their own list headers, in the page toolbar. It was
 * written out five times as a label wearing the search frame's classes, and
 * five copies of a field are five chances for one of them to lose the hidden
 * label or the 16px floor that keeps iOS from zooming the page on focus.
 *
 * It lives in the shell because its frame is the shell's search frame
 * (`.shell-search` in `shell.css`): the masthead's search button is drawn as
 * this field, so the two read as one kind of control.
 *
 * A real `<label>` around the input, so the whole frame is the target and the
 * label is the accessible name. The name is visually hidden rather than left
 * to the placeholder: a placeholder disappears the moment someone types, which
 * is when they most need to know what the field filters.
 */
export function FilterField({
  label,
  placeholder,
  value,
  onChange,
  id,
  className,
}: {
  /** What the field filters by, read as its accessible name. */
  label: string;
  /** The visible hint inside the empty field. */
  placeholder: string;
  value: string;
  onChange: (value: string) => void;
  /** For a caller that moves focus to the field. */
  id?: string;
  /** Sizes the field where it stands, e.g. inside a card header. */
  className?: string;
}) {
  return (
    <label className={className === undefined ? "shell-search" : `shell-search ${className}`}>
      <span className="sr-only">{label}</span>
      <SearchIcon />
      <input
        id={id}
        type="search"
        className="shell-search-input"
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}
