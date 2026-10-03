import type { ReactElement, ReactNode } from "react";
import { Select } from "../components/Select";

/**
 * Every select in the page toolbar, on every screen.
 *
 * The dashboard drew its filters as framed fields with a glyph and the
 * monitors and incidents screens drew theirs as a bare key beside a bordered
 * select, so one bar looked like two ideas of what a toolbar control is. A
 * filter, an order and a window are all this now: a frame, a decorative
 * glyph, the key, and a native select (DESIGN.md §8.4).
 *
 * It stays a native `<select>` inside a real `<label>`: the OS picker on a
 * phone, the keyboard behaviour, the listbox role and the label association
 * all come free, and these lists are a handful of values long. The label's
 * text is the key, so the key is the accessible name and needs no hidden
 * legend; the glyph is `aria-hidden` on the `<svg>` itself (icons.tsx).
 *
 * `ToolbarSelect.test.tsx` fails on a select in a toolbar built any other
 * way, so the two patterns cannot come back one screen at a time.
 */
export function ToolbarSelect({
  icon,
  label,
  value,
  onChange,
  selectClassName,
  facetKey,
  children,
}: {
  /** A glyph from components/icons.tsx that says what kind of control this is. */
  icon: ReactElement;
  /** The key, drawn in the frame and read as the control's name. */
  label: string;
  value: string | number;
  onChange: (value: string) => void;
  /** A hook for tests that must tell two kinds of select apart. */
  selectClassName?: string;
  /** Marks a tag filter by its key, which may also be an option's text. */
  facetKey?: string;
  children: ReactNode;
}) {
  return (
    <label className="tb-field" data-facet-key={facetKey}>
      {icon}
      <span className="tb-label">{label}</span>
      <Select
        className={selectClassName === undefined ? "tb-select" : `tb-select ${selectClassName}`}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      >
        {children}
      </Select>
    </label>
  );
}
