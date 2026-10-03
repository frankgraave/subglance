import { IconTag } from "../components/icons";
import { ToolbarSelect } from "../shell/ToolbarSelect";
import type { TagFacet, TagSelection } from "./model";

/**
 * One tag filter per tag key, for any screen that lists monitors.
 *
 * The dashboard and the inventory draw the same control from here, so a tag
 * filter looks and behaves the same wherever it appears: two lists that
 * filtered tags differently would answer "which monitors are prod" in two
 * ways.
 *
 * One `ToolbarSelect` per tag key, the toolbar's only select: a native
 * <select> in a framed label, so keyboard support, screen-reader semantics
 * and the OS picker on a phone come free. The key is the visible label, so the
 * control reads "env: prod" without a separate legend.
 */
export function TagFilters({
  facets,
  selected,
  onChange,
}: {
  facets: readonly TagFacet[];
  selected: TagSelection;
  onChange: (key: string, value: string) => void;
}) {
  return (
    <>
      {facets.map((facet) => (
        // The key is also the text of an option in the dashboard's Group by
        // control, so an explicit attribute — not the visible text — is what
        // identifies a facet unambiguously.
        <ToolbarSelect
          key={facet.key}
          facetKey={facet.key}
          icon={<IconTag />}
          label={facet.key}
          selectClassName="mon-facet-select"
          value={selected[facet.key] ?? ""}
          onChange={(value) => onChange(facet.key, value)}
        >
          {/* "Any" rather than a blank first option: an empty entry in a
              filter reads as a value someone forgot to name. */}
          <option value="">Any</option>
          {facet.values.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </ToolbarSelect>
      ))}
    </>
  );
}
