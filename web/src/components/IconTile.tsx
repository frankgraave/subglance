import type { ReactNode } from "react";

/**
 * The icon tile (DESIGN.md §8).
 *
 * 32x32, filled, no border. The design decision worth keeping is the missing
 * border: the tile is already a plane, so an edge around it would add a line
 * that distinguishes nothing while competing with the card edge beside it.
 *
 * The glyph is hidden from assistive technology by default. A tile next to a
 * monitor's name is a visual anchor, and announcing "globe" before the name
 * adds a word that carries no information. Pass `label` on the rare tile that
 * is the only thing saying what the row is.
 */

/** Tones a tile may take. `neutral` is the default and the common case; the
 *  status tones exist for a tile that stands in for a state. There is no
 *  accent tone on purpose: the accent is control colour and a tile is not a
 *  control, so an accent tile would say "interactive" about a mark you read. */
export type IconTileTone = "neutral" | "up" | "warn" | "down";

export type IconTileProps = {
  /** The glyph. Anything that renders; usually an inline SVG. */
  children: ReactNode;
  tone?: IconTileTone;
  /** Only when the tile itself is the label. Leaving it off hides the tile
   *  from assistive technology, which is right for a decorative anchor. */
  label?: string;
  className?: string;
};

export function IconTile({
  children,
  tone = "neutral",
  label,
  className,
}: IconTileProps) {
  return (
    <span
      className={className ? `icon-tile ${className}` : "icon-tile"}
      data-tone={tone}
      {...(label
        ? { role: "img", "aria-label": label }
        : { "aria-hidden": true })}
    >
      {children}
    </span>
  );
}
