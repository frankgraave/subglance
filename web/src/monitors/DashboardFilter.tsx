import { FilterPanel, type FilterGroup } from "./FilterPanel";
import { facetCounts, type TagFacet, type TagSelection } from "./model";
import type { Monitor, MonitorStatus } from "./types";

/**
 * The dashboard's Filter button and the panel it opens (SUB-183).
 *
 * Six tag selects in a bar became one button: the keys are listed in the
 * panel, each with its current value, and the chosen key's values are a
 * single-choice list with the number of monitors each would leave on screen.
 * Counts take the status tab, the text filter and the other keys into
 * account (`facetCounts`), so a value that would empty the list says so
 * before it is chosen. The panel itself is `FilterPanel`, which the monitors
 * inventory draws too.
 */
export type DashboardFilterProps = {
  monitors: readonly Monitor[];
  facets: readonly TagFacet[];
  selected: TagSelection;
  onSelect: (key: string, value: string) => void;
  onClearTags: () => void;
  status: MonitorStatus | null;
  query: string;
  onQueryChange: (query: string) => void;
  /** The phone layout: icon button, sheet, text filter inside. */
  narrow: boolean;
  /** How many monitors the filters leave, for the panel's foot. */
  visible: number;
};

export function DashboardFilter(props: DashboardFilterProps) {
  const { monitors, selected, status, query } = props;
  const groups: FilterGroup[] = props.facets.map((facet) => ({
    id: facet.key,
    legend: facet.key,
    facetKey: facet.key,
    value: selected[facet.key] ?? "",
    valueText: selected[facet.key] ?? "Any",
    options: () => {
      const counts = facetCounts(monitors, facet.key, status, query, selected);
      return [
        { value: "", text: "Any", count: counts.any },
        ...facet.values.map((value) => ({
          value,
          text: value,
          count: counts.values.get(value) ?? 0,
        })),
      ];
    },
    onChange: (value) => props.onSelect(facet.key, value),
  }));
  return (
    <FilterPanel
      label="Filter monitors"
      groups={groups}
      onClear={props.onClearTags}
      clearLabel="Clear tags"
      query={query}
      onQueryChange={props.onQueryChange}
      queryLabel="Filter monitors by name or address"
      queryPlaceholder="Name or address"
      narrow={props.narrow}
      visible={props.visible}
      total={monitors.length}
      noun="monitor"
    />
  );
}
