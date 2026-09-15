import type { ReactElement } from "react";
import {
  IconColumnsAuto,
  IconColumnsOne,
  IconColumnsThree,
  IconColumnsTwo,
} from "../components/icons";
import { SegmentedControl } from "../components/SegmentedControl";
import { CARD_COLUMNS, type CardColumns } from "./preferences";

/**
 * How many cards the Cards layout puts on a row.
 *
 * **Why this exists.** At one card per row the layout spent most of its width
 * on nothing: the heartbeat was pushed against the right edge with a dashed
 * rule crossing the empty middle. A monitor card does not need 1200px, and on
 * a wide screen the choice between "big tiles" and "see everything" is a
 * preference rather than something a breakpoint can decide.
 *
 * **Why icons and not numbers.** The options *are* shapes, so the button shows
 * the layout it selects instead of naming it — and the glyphs survive
 * translation where "2" beside "3" still needs a column of text next to it.
 * The cost is that a bar chart is only obvious once you know what the control
 * does, which is why every option carries both a `label` (the accessible name)
 * and a `hint` (the tooltip). An icon-only control without those is a row of
 * buttons a screen reader calls "button".
 *
 * **Why it is only rendered for Cards.** A setting that is visible while it
 * does nothing teaches people it does nothing. Rows, Compact and the wall have
 * their own shapes; this belongs to one layout and appears with it.
 */

const ICONS: Record<CardColumns, ReactElement> = {
  "1": <IconColumnsOne />,
  "2": <IconColumnsTwo />,
  "3": <IconColumnsThree />,
  auto: <IconColumnsAuto />,
};

const LABELS: Record<CardColumns, string> = {
  "1": "One per row",
  "2": "Two per row",
  "3": "Three per row",
  auto: "Fill the width",
};

export type CardColumnsSwitcherProps = {
  value: CardColumns;
  onChange: (next: CardColumns) => void;
};

export function CardColumnsSwitcher({
  value,
  onChange,
}: CardColumnsSwitcherProps) {
  return (
    <SegmentedControl
      label="Cards per row"
      options={CARD_COLUMNS.map((option) => ({
        id: option.id,
        label: LABELS[option.id],
        icon: ICONS[option.id],
        hint: option.hint,
      }))}
      value={value}
      onChange={onChange}
    />
  );
}
