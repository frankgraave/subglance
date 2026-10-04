import { useId, useState } from "react";
import { Radio } from "../components/Choice";
import { IconFilter } from "../components/icons";
import { Popover } from "../components/Popover";
import { FilterField } from "../shell/FilterField";

/**
 * A list's Filter button and the panel it opens: the dashboard's and the
 * monitors inventory's, drawn by one component so a filter reads the same on
 * both screens (SUB-183, SUB-207).
 *
 * The groups are listed in the panel, each with its current value, and the
 * chosen group's values are a single-choice list with the number of rows each
 * would leave on screen. A value that would empty the list says so before it
 * is chosen.
 *
 * On a phone the panel is a sheet from the bottom edge (`.mon-sheet` in
 * monitors.css) that also holds the text filter, which has no room in the
 * list's header there, and every group's values one under another in rows a
 * thumb can hit.
 */

export type FilterOption = { value: string; text: string; count: number };

export type FilterGroup = {
  /** Stable identity: a tag key and a built-in group may share a name. */
  id: string;
  /** The group's name, drawn as its legend. */
  legend: string;
  /** Set on a tag group: tests and the facet columns find it by key. */
  facetKey?: string;
  /** The chosen value; "" is the group's "any" option. */
  value: string;
  /** What the chosen value reads as, for the group's row in the key column. */
  valueText: string;
  /**
   * The values, the "any" option first, each with the number of rows it
   * would leave. A function, so only the group on screen is counted.
   */
  options: () => readonly FilterOption[];
  onChange: (value: string) => void;
};

export type FilterPanelProps = {
  /** The dialog's accessible name. */
  label: string;
  groups: readonly FilterGroup[];
  /** Drops every group's choice; the text filter is the field's own. */
  onClear: () => void;
  clearLabel: string;
  query: string;
  onQueryChange: (query: string) => void;
  /** The text filter's accessible name, in the phone sheet. */
  queryLabel: string;
  queryPlaceholder: string;
  /** The phone layout: icon button, sheet, text filter inside. */
  narrow: boolean;
  /** How many rows the filters leave, and of how many, for the foot. */
  visible: number;
  total: number;
  /** The noun the sheet's button counts: "monitor". */
  noun: string;
};

export function FilterPanel(props: FilterPanelProps) {
  const { groups, query, narrow } = props;
  const chosen = groups.filter((group) => group.value !== "").length;
  // On a phone the text filter is in this sheet, so it counts on the badge
  // that says how much is narrowing the list.
  const active = chosen + (narrow && query.trim() !== "" ? 1 : 0);
  const label = active > 0 ? `Filter, ${active} active` : "Filter";
  return (
    <Popover
      label={props.label}
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

type Inner = FilterPanelProps & { close: () => void };

/** One group's values as radios, the "any" option first, each with its count. */
function Values({ group }: { group: FilterGroup }) {
  const name = useId();
  return (
    <fieldset className="mon-values" data-facet-key={group.facetKey}>
      <legend className="mon-values-key">{group.legend}</legend>
      {group.options().map((option) => (
        <Radio
          key={option.value}
          name={name}
          checked={group.value === option.value}
          onChange={() => group.onChange(option.value)}
        >
          <span className="mon-option" data-empty={option.count === 0}>
            {/* The space is for the accessible name ("prod 2", not "prod2");
                flex layout drops it from the drawing. */}
            <span className="mon-option-text">{option.text}</span>{" "}
            <span className="mon-option-count">{option.count}</span>
          </span>
        </Radio>
      ))}
    </fieldset>
  );
}

function FilterColumns(props: Inner) {
  const { groups } = props;
  // Opens on the first group that is filtering, so a reader who comes back to
  // change a choice lands on it rather than on the first group in the list.
  const [id, setId] = useState(
    () => groups.find((group) => group.value !== "")?.id ?? groups[0]?.id,
  );
  const group = groups.find((candidate) => candidate.id === id) ?? groups[0];
  return (
    <>
      <div className="mon-filter-cols">
        <ul className="mon-filter-keys">
          {groups.map((candidate) => (
            <li key={candidate.id}>
              <button
                type="button"
                className="mon-filter-key"
                data-facet-key={candidate.facetKey}
                aria-pressed={candidate.id === group?.id}
                onClick={() => setId(candidate.id)}
              >
                <span className="mon-option-text">{candidate.legend}</span>{" "}
                <span className="mon-filter-key-value">{candidate.valueText}</span>
              </button>
            </li>
          ))}
        </ul>
        {group === undefined ? null : <Values group={group} />}
      </div>
      <div className="mon-panel-foot">
        <button
          type="button"
          className="button button--quiet button--compact"
          onClick={props.onClear}
        >
          {props.clearLabel}
        </button>
        <span className="mon-panel-count">
          {props.visible} of {props.total} {props.noun}s
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
        label={props.queryLabel}
        placeholder={props.queryPlaceholder}
        value={props.query}
        onChange={props.onQueryChange}
      />
      {props.groups.map((group) => (
        <Values key={group.id} group={group} />
      ))}
      <button
        type="button"
        className="button button--primary mon-sheet-done"
        onClick={props.close}
      >
        Show {props.visible} {props.visible === 1 ? props.noun : `${props.noun}s`}
      </button>
    </>
  );
}
