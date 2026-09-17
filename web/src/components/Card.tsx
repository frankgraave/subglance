import { useId, type ReactNode } from "react";
import { IconTile } from "./IconTile";

/**
 * A card: the frame that groups panels (DESIGN.md §2.7).
 *
 * The pattern this encodes is the one thing that makes a screen look like the
 * rest of the product, and it is easy to get subtly wrong by hand — so it
 * lives in a component rather than in a convention.
 *
 * Three rules, measured rather than chosen:
 *
 * 1. The card is *padding with an edge*, not a panel. Its fill is quieter than
 *    the panels inside it and it holds them 6px clear of its own border, so it
 *    reads as the surface they rest on. A frame drawn at the same weight as
 *    its contents produces two competing edges, which is the double-framing
 *    that got an earlier version of this frame deleted entirely.
 *
 * 2. The header is 46px and its padding is asymmetric — 8px around, 6px at the
 *    bottom. The tighter bottom is not a rounding error: the panel below is
 *    already 6px from the card's edge, so an 8px gap under the title would
 *    read as more space than the one beside it.
 *
 * 3. The title is sans at 16px/500, sentence case. Not mono, not uppercase.
 *    Mono-caps is the register for labels *inside* a panel; a card title is a
 *    heading and takes the text face. Using one role for both is what made our
 *    headers read as a row of shouted abbreviations.
 */

export type CardProps = {
  /** The heading. Sentence case; it is a title, not a label. */
  title: ReactNode;
  /** Optional glyph for the tile that anchors the header's left. */
  icon?: ReactNode;
  /** Optional control on the header's right: a link, a button, a menu. */
  action?: ReactNode;
  /**
   * A quiet line under the title: a count, a qualifier, a caveat.
   *
   * It exists so a card that *is* a page heading can carry what used to sit
   * under the page title (SUB-138) without inventing a second heading. Not a
   * subtitle — it is helper text, and it never competes with the title.
   */
  note?: ReactNode;
  /** The panels. */
  children: ReactNode;
  /**
   * Heading level. A card's title is a real heading in the document outline,
   * so the level has to fit where the card sits rather than always being h2.
   */
  headingLevel?: 1 | 2 | 3 | 4;
  className?: string;
  /** Marks the card's own region for assistive technology. */
  "aria-label"?: string;
};

export function Card({
  title,
  icon,
  action,
  note,
  children,
  headingLevel = 2,
  className,
  "aria-label": ariaLabel,
}: CardProps) {
  const Heading = `h${headingLevel}` as "h1" | "h2" | "h3" | "h4";
  // The section is named by its own heading rather than by a repeated string.
  // `useId` rather than a caller-supplied id: two tag values differing only in
  // case used to collide into one id and silently break the association, and a
  // generated id cannot collide at all.
  const headingId = useId();
  return (
    <section
      className={className ? `card ${className}` : "card"}
      aria-labelledby={headingId}
      {...(ariaLabel ? { "aria-label": ariaLabel } : {})}
    >
      <div className="card-head">
        <div className="card-head-lead">
          {icon ? <IconTile>{icon}</IconTile> : null}
          <div className="card-head-text">
            <Heading id={headingId} className="card-title">
              {title}
            </Heading>
            {note === undefined ? null : (
              <p className="card-note">{note}</p>
            )}
          </div>
        </div>
        {action ? <div className="card-head-action">{action}</div> : null}
      </div>
      {children}
    </section>
  );
}

/**
 * A panel: what goes inside a card.
 *
 * Carries the tighter radius and a fill one step louder than the card, which
 * is the whole of the nesting illusion. Padding is opt-out because a panel
 * holding a list wants its rows flush to its own edge, while a panel holding
 * prose wants breathing room.
 */
export type PanelProps = {
  children: ReactNode;
  /** Optional label, in the mono-caps register that belongs inside panels. */
  label?: ReactNode;
  /** Set false for a panel whose child draws to the edges, e.g. a list. */
  padded?: boolean;
  className?: string;
};

export function Panel({
  children,
  label,
  padded = true,
  className,
}: PanelProps) {
  const classes = ["panel"];
  if (!padded) classes.push("panel--flush");
  if (className) classes.push(className);
  return (
    <div className={classes.join(" ")}>
      {label ? <p className="panel-label">{label}</p> : null}
      {children}
    </div>
  );
}
