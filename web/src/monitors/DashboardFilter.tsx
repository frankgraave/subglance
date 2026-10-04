import { useId, useState } from "react";
import { Radio } from "../components/Choice";
import { IconFilter } from "../components/icons";
import { Popover } from "../components/Popover";
import { FilterField } from "../shell/FilterField";
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
 * before it is chosen.
 *
 * On a phone the panel is a sheet from the bottom edge (`.mon-sheet` in
 * monitors.css) that also holds the text filter, which has no room in the
 * list's header there, and every key's values one under another in rows a
 * thumb can hit.
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
  const { selected, query, narrow } = props;
  const tags = Object.keys(selected).length;
  // On a phone the text filter is in this sheet, so it counts on the badge
  // that says how much is narrowing the list.
  const active = tags + (narrow && query.trim() !== "" ? 1 : 0);
  const label = active > 0 ? `Filter, ${active} active` : "Filter";
  return (
    <Popover
      label="Filter monitors"
      triggerLabel={label}
      triggerClassName="button button--compact mon-head-button"
      className={narrow ? "mon-panel mon-sheet" : "mon-panel mon-filter-panel"}
      trigger={
        <>
          {narrow ? null : <span>Filter</span>}
          <IconFilter />
          {active > 0 ? (
            <span className="chip chip--count mon-head-badge">{active}</span>
          ) : null}
        </>
      }
    >
      {(close) =>
        narrow ? (
          <FilterSheet {...props} close={close} />
        ) : (
          <FilterColumns {...props} close={close} />
        )
      }
    </Popover>
  );
}

type Inner = DashboardFilterProps & { close: () => void };

/** One key's values as radios, “Any” first, each with its count. */
function Values({
  facet,
  props,
}: {
  facet: TagFacet;
  props: DashboardFilterProps;
}) {
  const name = useId();
  const counts = facetCounts(
    props.monitors,
    facet.key,
    props.status,
    props.query,
    props.selected,
  );
  const current = props.selected[facet.key] ?? "";
  const option = (value: string, text: string, count: number) => (
    <Radio
      key={value}
      name={name}
      checked={current === value}
      onChange={() => props.onSelect(facet.key, value)}
    >
      <span className="mon-option" data-empty={count === 0}>
        {/* The space is for the accessible name ("prod 2", not "prod2");
            flex layout drops it from the drawing. */}
        <span className="mon-option-text">{text}</span>{" "}
        <span className="mon-option-count">{count}</span>
      </span>
    </Radio>
  );
  return (
    <fieldset className="mon-values" data-facet-key={facet.key}>
      <legend className="mon-values-key">{facet.key}</legend>
      {option("", "Any", counts.any)}
      {facet.values.map((value) =>
        option(value, value, counts.values.get(value) ?? 0),
      )}
    </fieldset>
  );
}

function FilterColumns(props: Inner) {
  const { facets, selected } = props;
  // Opens on the first key that is filtering, so a reader who comes back to
  // change a choice lands on it rather than on the alphabet's first key.
  const [key, setKey] = useState(
    () => facets.find((facet) => selected[facet.key] !== undefined)?.key ??
      facets[0]?.key,
  );
  const facet = facets.find((candidate) => candidate.key === key) ?? facets[0];
  return (
    <>
      <div className="mon-filter-cols">
        <ul className="mon-filter-keys">
          {facets.map((candidate) => (
            <li key={candidate.key}>
              <button
                type="button"
                className="mon-filter-key"
                data-facet-key={candidate.key}
                aria-pressed={candidate.key === facet?.key}
                onClick={() => setKey(candidate.key)}
              >
                <span className="mon-option-text">{candidate.key}</span>{" "}
                <span className="mon-filter-key-value">
                  {selected[candidate.key] ?? "Any"}
                </span>
              </button>
            </li>
          ))}
        </ul>
        {facet === undefined ? null : <Values facet={facet} props={props} />}
      </div>
      <div className="mon-panel-foot">
        <button
          type="button"
          className="button button--quiet button--compact"
          onClick={props.onClearTags}
        >
          Clear tags
        </button>
        <span className="mon-panel-count">
          {props.visible} of {props.monitors.length} monitors
        </span>
        <button
          type="button"
          className="button button--compact"
          onClick={props.close}
        >
          Done
        </button>
      </div>
    </>
  );
}

function FilterSheet(props: Inner) {
  return (
    <>
      <FilterField
        label="Filter monitors by name or address"
        placeholder="Name or address"
        value={props.query}
        onChange={props.onQueryChange}
      />
      {props.facets.map((facet) => (
        <Values key={facet.key} facet={facet} props={props} />
      ))}
      <button
        type="button"
        className="button button--primary mon-sheet-done"
        onClick={props.close}
      >
        Show {props.visible} {props.visible === 1 ? "monitor" : "monitors"}
      </button>
    </>
  );
}
