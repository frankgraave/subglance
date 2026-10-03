import { IconTag } from "../components/icons";
import type { TagFacet, TagSelection } from "./model";

/**
 * One tag filter per tag key, for any screen that lists monitors.
 *
 * The dashboard and the inventory draw the same control from here, so a tag
 * filter looks and behaves the same wherever it appears: two lists that
 * filtered tags differently would answer "which monitors are prod" in two
 * ways.
 *
 * One native <select> per tag key, and native on purpose: a custom listbox
 * would have to re-earn keyboard support, screen-reader semantics and the OS
 * picker on a phone, and these lists are a handful of values long — the case
 * where a native select is simply better. The key is the visible label, so the
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
        <label
          key={facet.key}
          className="tb-field tb-field--framed"
          data-facet-key={facet.key}
        >
          {/* The glyph, and it is decorative: the <label> around the select is
              already the control's accessible name, so an icon that announced
              itself would make a screen reader say the filter twice. */}
          <IconTag />
          <span className="tb-label">{facet.key}</span>
          <select
            className="tb-select mon-facet-select"
            value={selected[facet.key] ?? ""}
            onChange={(event) => onChange(facet.key, event.target.value)}
          >
            {/* "Any" rather than a blank first option: an empty entry in a
                filter reads as a value someone forgot to name. */}
            <option value="">Any</option>
            {facet.values.map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>
      ))}
    </>
  );
}
