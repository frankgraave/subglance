import type { CSSProperties } from "react";
import { Card } from "../components/Card";
import { IconAlert, IconList } from "../components/icons";
import type { CardColumns } from "../shell/preferences";
import { CARD_BEAT_WIDTH, MonitorCard } from "./MonitorCard";
import { EmptyState } from "./EmptyState";
import { partition, sectionsByTag } from "./model";
import type { Monitor } from "./types";

/**
 * The monitor list as stacked cards, for phone-width viewports.
 *
 * A `<ul>` rather than a reflowed table. The CSS-only trick — keeping one
 * `<table>` and switching the cells to `display: block` on mobile — is popular
 * and wrong here: setting `display` on a table element drops the table's
 * semantics in Safari, which is precisely the failure `MonitorTable`'s comment
 * warns about. A list of items is also the honest description of what this is:
 * on a phone you read one monitor, you do not compare a column across 200.
 *
 * The ordering rule is shared with the table (`partition`), so "needs
 * attention first, then alphabetical" means the same thing on both screens.
 * The section headings are real `<h2>`s here rather than table row headers, so
 * a screen reader can jump between them.
 */

export type MonitorCardListProps = {
  monitors: readonly Monitor[];
  /** Non-empty when the list has been filtered, used only for empty-state copy. */
  query?: string;
  /** Total before filtering, so "no results" can be told from "no monitors". */
  totalCount?: number;
  /** True when a filter other than the query is narrowing the list. */
  filtered?: boolean;
  /** Tag key to group by, or null for the flat attention/all split. */
  groupKey?: string | null;
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
  /**
   * How many cards on a row. Defaults to one, so a caller that has no opinion
   * gets the single column this layout has always been.
   */
  columns?: CardColumns;
  /** Opens a monitor's detail view client-side. See MonitorLink. */
  onOpen?: (id: string) => void;
  /**
   * True when the live stream is dead. Forwarded to every card so its status
   * word moves into the past tense (DESIGN.md §6).
   */
  stale?: boolean;
};

export function MonitorCardList({
  monitors,
  query = "",
  totalCount,
  beatWidth = CARD_BEAT_WIDTH,
  filtered = false,
  groupKey = null,
  columns = "1",
  onOpen,
  stale = false,
}: MonitorCardListProps) {
  const total = totalCount ?? monitors.length;

  /*
   * The column count reaches CSS as a variable, and "auto" reaches it as an
   * attribute instead.
   *
   * Two channels because they are two different rules: a fixed count is
   * arithmetic the grid can do with `repeat()`, while auto-fill is a different
   * `grid-template-columns` altogether. Encoding auto as a number would mean
   * inventing one, and any number is the wrong answer on some window.
   */
  const stackProps = {
    "data-cols": columns,
    style:
      columns === "auto"
        ? undefined
        : ({ "--mon-card-cols": columns } as CSSProperties),
  };

  if (monitors.length === 0) {
    return <EmptyState query={query} totalCount={total} filtered={filtered} />;
  }

  const cards = (list: readonly Monitor[]) =>
    list.map((monitor) => (
      <MonitorCard
        key={monitor.id}
        monitor={monitor}
        beatWidth={beatWidth}
        onOpen={onOpen}
        stale={stale}
      />
    ));

  if (groupKey !== null) {
    return (
      <div className="mon-cards">
        {sectionsByTag(monitors, groupKey).map((section) => (
          <Card
            key={section.id}
            className="mon-cards-section"
            title={`${section.label} (${section.monitors.length})`}
            icon={<IconList />}
            headingLevel={3}
          >
            <ul className="mon-card-stack" {...stackProps}>
              {cards(section.monitors)}
            </ul>
          </Card>
        ))}
      </div>
    );
  }

  const { attention, rest } = partition(monitors);

  return (
    <div className="mon-cards">
      {attention.length > 0 && (
        <Card
          className="mon-cards-section"
          title={`Needs attention (${attention.length})`}
          icon={<IconAlert />}
          headingLevel={3}
        >
          <ul className="mon-card-stack" {...stackProps}>
            {cards(attention)}
          </ul>
        </Card>
      )}

      <Card
        className="mon-cards-section"
        title={
          attention.length > 0
            ? `All monitors (${rest.length})`
            : `Monitors (${rest.length})`
        }
        icon={<IconList />}
        headingLevel={3}
      >
        <ul className="mon-card-stack" {...stackProps}>
          {cards(rest)}
        </ul>
      </Card>
    </div>
  );
}
