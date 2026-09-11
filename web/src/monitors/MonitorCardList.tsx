import { CARD_BEAT_WIDTH, MonitorCard } from "./MonitorCard";
import { EmptyState } from "./EmptyState";
import { partition } from "./model";
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
  /** Explicit heartbeat width; required in jsdom, which has no layout. */
  beatWidth?: number;
};

export function MonitorCardList({
  monitors,
  query = "",
  totalCount,
  beatWidth = CARD_BEAT_WIDTH,
}: MonitorCardListProps) {
  const total = totalCount ?? monitors.length;

  if (monitors.length === 0) {
    return <EmptyState query={query} totalCount={total} />;
  }

  const { attention, rest } = partition(monitors);

  const cards = (list: readonly Monitor[]) =>
    list.map((monitor) => (
      <MonitorCard key={monitor.id} monitor={monitor} beatWidth={beatWidth} />
    ));

  return (
    <div className="mon-cards">
      {attention.length > 0 && (
        <section className="mon-cards-section" aria-labelledby="mon-cards-attention">
          <h3 id="mon-cards-attention" className="mon-cards-title">
            Needs attention ({attention.length})
          </h3>
          <ul className="mon-card-stack">{cards(attention)}</ul>
        </section>
      )}

      <section className="mon-cards-section" aria-labelledby="mon-cards-all">
        <h3 id="mon-cards-all" className="mon-cards-title">
          {attention.length > 0 ? `All monitors (${rest.length})` : `Monitors (${rest.length})`}
        </h3>
        <ul className="mon-card-stack">{cards(rest)}</ul>
      </section>
    </div>
  );
}
