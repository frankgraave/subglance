import { useId, type ReactNode } from "react";

/**
 * One section of the dashboard's list, under its own heading (SUB-183).
 *
 * The sections used to be cards of their own inside the dashboard's card, or
 * the only card on the page, depending on the layout. Now the dashboard draws
 * one card around the whole list and heads it with the status tabs, so a
 * section is what it always meant: a part of that list, headed in the legend
 * register the table's own section rows use (`.mon-section-title`). A card
 * inside a card was a second frame at the same weight as the first.
 *
 * `h3`, under the card's `h2`. Named by its heading through a generated id,
 * so two tag values differing only in case cannot collide.
 */
export function MonitorGroup({
  title,
  className,
  children,
}: {
  title: string;
  /** A hook for the layout that wears it, e.g. the attention section. */
  className?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <section
      className={className ? `mon-group ${className}` : "mon-group"}
      aria-labelledby={id}
    >
      <h3 id={id} className="mon-group-title">
        {title}
      </h3>
      {children}
    </section>
  );
}
