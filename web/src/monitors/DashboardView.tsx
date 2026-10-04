import { useId } from "react";
import { useCompactViewport } from "../layout/useMediaQuery";
import { IconLayout } from "../components/icons";
import { Popover } from "../components/Popover";
import { Select } from "../components/Select";
import { CardColumnsSwitcher } from "../shell/CardColumnsSwitcher";
import { LayoutSwitcher } from "../shell/LayoutSwitcher";
import { LAYOUTS, type CardColumns, type LayoutId } from "../shell/preferences";
import type { TagFacet } from "./model";

/**
 * The dashboard's View button and its panel (SUB-183): how the list is drawn
 * and arranged, never which monitors are in it.
 *
 * The button says the current state ("Rows · by team"), so the arrangement
 * is readable without opening it. The panel is a label column beside the
 * choices: the layout, cards per row (only while Cards is on screen, as
 * before) and the grouping key.
 *
 * On a phone Rows and Compact are drawn as cards (`effectiveLayout`), so the
 * panel offers Cards and the status wall only, and no cards per row: below
 * the card grid's 380px floor there is one column whatever is chosen. A
 * control that changes nothing at that width is not offered there.
 */
export type DashboardViewProps = {
  /** The layout on screen, after the viewport's veto. */
  shown: LayoutId;
  onLayoutChange?: (next: LayoutId) => void;
  cardColumns: CardColumns;
  onCardColumnsChange?: (next: CardColumns) => void;
  /** The grouping choice; absent before the list has loaded. */
  grouping?: {
    facets: readonly TagFacet[];
    value: string | null;
    onChange: (next: string | null) => void;
  };
};

const PHONE_LAYOUTS: readonly LayoutId[] = ["cards", "wall"];

export function DashboardView({
  shown,
  onLayoutChange,
  cardColumns,
  onCardColumnsChange,
  grouping,
}: DashboardViewProps) {
  const groupId = useId();
  // Read here rather than passed, so the loading notice can draw the same
  // button without owning the viewport query.
  const narrow = useCompactViewport();
  const layoutName = LAYOUTS.find((option) => option.id === shown)?.label ?? shown;
  const group = grouping?.value ?? null;
  const face = group === null ? layoutName : `${layoutName} \u00b7 by ${group}`;
  const label = group === null
    ? `View: ${layoutName}`
    : `View: ${layoutName}, grouped by ${group}`;
  const groupable = grouping !== undefined && grouping.facets.length > 0;
  if (onLayoutChange === undefined && !groupable) return null;
  return (
    <Popover
      label="View"
      triggerLabel={label}
      triggerClassName="button button--compact mon-head-button"
      className={narrow ? "mon-panel mon-sheet" : "mon-panel mon-view-panel"}
      trigger={
        <>
          {narrow ? null : <span>{face}</span>}
          <IconLayout />
        </>
      }
    >
      {(close) => (
        <>
          <div className="mon-view-grid">
            {onLayoutChange === undefined ? null : (
              <>
                <span className="mon-view-label" aria-hidden="true">
                  Layout
                </span>
                <LayoutSwitcher
                  layout={shown}
                  onChange={onLayoutChange}
                  only={narrow ? PHONE_LAYOUTS : undefined}
                />
              </>
            )}
            {!narrow && shown === "cards" && onCardColumnsChange !== undefined ? (
              <>
                <span className="mon-view-label" aria-hidden="true">
                  Per row
                </span>
                <CardColumnsSwitcher
                  value={cardColumns}
                  onChange={onCardColumnsChange}
                />
              </>
            ) : null}
            {groupable ? (
              <>
                <label className="mon-view-label" htmlFor={groupId}>
                  Group by
                </label>
                {/* A native select: seven options is a list, not a row of
                    segments, and the phone gets its own picker for free. */}
                <Select
                  id={groupId}
                  className="input input--fit mon-group-select"
                  value={group ?? ""}
                  onChange={(event) =>
                    grouping.onChange(event.target.value || null)
                  }
                >
                  <option value="">None</option>
                  {grouping.facets.map((facet) => (
                    <option key={facet.key} value={facet.key}>
                      {facet.key}
                    </option>
                  ))}
                </Select>
              </>
            ) : null}
          </div>
          {narrow ? (
            <button
              type="button"
              className="button button--primary mon-sheet-done"
              onClick={close}
            >
              Done
            </button>
          ) : null}
        </>
      )}
    </Popover>
  );
}
