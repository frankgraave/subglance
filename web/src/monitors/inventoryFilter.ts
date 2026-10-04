import { filterByTags, filterMonitors, type TagFacet, type TagSelection } from "./model";
import { filterByType, type InventoryMonitor } from "./inventory";
import type { FilterGroup, FilterOption } from "./FilterPanel";

/**
 * What narrows the monitors inventory, in one value: the text filter, the
 * type, paused or not, and the tags.
 */
export type InventoryChoice = {
  query: string;
  /** "" is every type. */
  type: string;
  /** "" is every monitor, "active" or "paused". */
  paused: string;
  tags: TagSelection;
};

/** The types the inventory offers, in the add form's order. */
export const INVENTORY_TYPES: readonly { value: string; text: string }[] = [
  { value: "http", text: "HTTP" },
  { value: "tcp", text: "TCP" },
  { value: "ping", text: "Ping" },
  { value: "ssl", text: "SSL" },
  { value: "push", text: "Push" },
];

/*
 * "Active only" and "Paused only", the words the select used: the dashboard
 * hides paused monitors and this page shows them, so a reader who narrows to
 * them is told plainly that is what the list now holds.
 */
export const INVENTORY_PAUSED: readonly { value: string; text: string }[] = [
  { value: "active", text: "Active only" },
  { value: "paused", text: "Paused only" },
];

function filterByPaused(
  monitors: readonly InventoryMonitor[],
  paused: string,
): InventoryMonitor[] {
  if (paused === "active") return monitors.filter((m) => m.enabled);
  if (paused === "paused") return monitors.filter((m) => !m.enabled);
  return [...monitors];
}

/** Which group to leave out, so its own values can be counted. */
type Except = "type" | "paused" | { tag: string } | null;

/** The monitors every choice but `except` leaves on screen. */
export function applyInventoryChoice(
  monitors: readonly InventoryMonitor[],
  choice: InventoryChoice,
  except: Except = null,
): InventoryMonitor[] {
  const tags =
    except !== null && typeof except === "object"
      ? Object.fromEntries(
          Object.entries(choice.tags).filter(([key]) => key !== except.tag),
        )
      : choice.tags;
  let out = filterMonitors(monitors, choice.query);
  if (except !== "type") out = filterByType(out, choice.type);
  if (except !== "paused") out = filterByPaused(out, choice.paused);
  return filterByTags(out, tags);
}

const option = (value: string, text: string, count: number): FilterOption => ({
  value,
  text,
  count,
});

/**
 * The inventory's filter groups for `FilterPanel`: type, paused, then one per
 * tag key. Each value carries the number of monitors it would leave, counted
 * with every other choice and the text filter applied, the way the
 * dashboard's tag counts are (`facetCounts`).
 */
export function inventoryFilterGroups(
  monitors: readonly InventoryMonitor[],
  facets: readonly TagFacet[],
  choice: InventoryChoice,
  set: {
    type: (value: string) => void;
    paused: (value: string) => void;
    tag: (key: string, value: string) => void;
  },
): FilterGroup[] {
  const textOf = (list: readonly { value: string; text: string }[], value: string) =>
    list.find((entry) => entry.value === value)?.text ?? "Any";
  return [
    {
      id: "type",
      legend: "Type",
      value: choice.type,
      valueText: textOf(INVENTORY_TYPES, choice.type),
      options: () => {
        const pool = applyInventoryChoice(monitors, choice, "type");
        return [
          option("", "Any", pool.length),
          ...INVENTORY_TYPES.map((entry) =>
            option(entry.value, entry.text, pool.filter((m) => m.type === entry.value).length),
          ),
        ];
      },
      onChange: set.type,
    },
    {
      id: "paused",
      legend: "Paused",
      value: choice.paused,
      valueText: textOf(INVENTORY_PAUSED, choice.paused),
      options: () => {
        const pool = applyInventoryChoice(monitors, choice, "paused");
        return [
          option("", "Any", pool.length),
          ...INVENTORY_PAUSED.map((entry) =>
            option(entry.value, entry.text, filterByPaused(pool, entry.value).length),
          ),
        ];
      },
      onChange: set.paused,
    },
    ...facets.map((facet): FilterGroup => ({
      // Prefixed: a tag key may be called "type" or "paused" too.
      id: `tag:${facet.key}`,
      legend: facet.key,
      facetKey: facet.key,
      value: choice.tags[facet.key] ?? "",
      valueText: choice.tags[facet.key] ?? "Any",
      options: () => {
        const pool = applyInventoryChoice(monitors, choice, { tag: facet.key });
        return [
          option("", "Any", pool.length),
          ...facet.values.map((value) =>
            option(value, value, pool.filter((m) => m.tags[facet.key] === value).length),
          ),
        ];
      },
      onChange: (value) => set.tag(facet.key, value),
    })),
  ];
}
