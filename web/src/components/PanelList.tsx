import type { ReactNode } from "react";

/**
 * A list that reads as a stack of panels rather than as a table (DESIGN.md §10).
 *
 * The defect this closes is the single largest layout difference against the
 * reference style: a list drawn with one shared border per gap reads as a
 * spreadsheet, and a spreadsheet is something you scan, not something you act
 * on. Giving every row its own edge, its own corner and a little air around it
 * turns the same data into a column of objects — each one obviously a thing
 * you can point at, hover, and press a button on.
 *
 * Three decisions are load-bearing and easy to undo by accident:
 *
 *  1. **No shared dividers.** A row owns its whole border. A `border-bottom`
 *     added to `.panel-row` later would put the grid straight back.
 *  2. **The surface does not move on hover.** No translate, no scale. This is
 *     a screen people sit and stare at; rows that jump as the pointer crosses
 *     them make the list unreadable while it is being read. Hover is expressed
 *     on the icon (it lights up), on the label (it follows) and by bringing
 *     the row's actions forward.
 *  3. **Actions are revealed, never removed.** They fade in on hover and on
 *     `:focus-within`, and they stay in the layout at all times — both so the
 *     row cannot resize under the pointer and so a keyboard user can still
 *     reach them.
 */

export type PanelRowProps = {
  /**
   * The leading mark: a lamp, a tile, a glyph. Rendered in its own slot
   * because hover is expressed here rather than on the surface.
   */
  icon?: ReactNode;
  /** The row's subject — usually a link carrying the name. */
  children: ReactNode;
  /**
   * Buttons that act on this row. They are brought forward on hover and on
   * focus-within; see the note above about why they are never unmounted.
   */
  actions?: ReactNode;
  /** Status, tone or any other flag a stylesheet wants to key off. */
  status?: string;
  className?: string;
  "data-testid"?: string;
};

/** One row: a panel with its own border, corner and breathing room. */
export function PanelRow({
  icon,
  children,
  actions,
  status,
  className,
  "data-testid": testId,
}: PanelRowProps) {
  return (
    <li
      className={join("panel-row", className)}
      {...(status === undefined ? {} : { "data-status": status })}
      {...(testId === undefined ? {} : { "data-testid": testId })}
    >
      {icon === undefined ? null : (
        // aria-hidden is deliberately NOT set here: the caller decides, because
        // in this product the leading mark is often the status lamp, which is
        // the row's most important fact rather than decoration.
        <span className="panel-row-icon">{icon}</span>
      )}
      <div className="panel-row-body">{children}</div>
      {actions === undefined ? null : (
        <div className="panel-row-actions">{actions}</div>
      )}
    </li>
  );
}

export type PanelListProps = {
  children: ReactNode;
  /** The list's accessible name. A stack of panels is still a list. */
  label?: string;
  className?: string;
};

/** The stack. A real `<ul>`, so the item count still matches what is on screen. */
export function PanelList({ children, label, className }: PanelListProps) {
  return (
    <ul
      className={join("panel-list", className)}
      {...(label === undefined ? {} : { "aria-label": label })}
    >
      {children}
    </ul>
  );
}

function join(base: string, extra?: string): string {
  return extra ? `${base} ${extra}` : base;
}
