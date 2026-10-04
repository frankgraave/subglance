import { Card } from "../components/Card";
import { IconList } from "../components/icons";
import type { CardColumns, LayoutId } from "../shell/preferences";
import { DashboardView } from "./DashboardView";

/**
 * The dashboard's card while the first list is loading or failed to load.
 *
 * The same card the list arrives in, so nothing moves when it does, with the
 * View button in its header: the loading and failed moments are the ones
 * that most need a way to the status wall, which is built to carry exactly
 * this sentence in its frame (SUB-182). Only View: a filter over a list that
 * has not arrived has nothing to narrow.
 */
export function DashboardNotice({
  notice,
  failed,
  layout,
  onLayoutChange,
  cardColumns,
}: {
  notice: string;
  /** Announced as an alert, not as a status line. */
  failed: boolean;
  layout: LayoutId;
  onLayoutChange?: (next: LayoutId) => void;
  cardColumns: CardColumns;
}) {
  return (
    <section className="mon-dashboard">
      <Card
        className="mon-board"
        title="Monitors"
        icon={<IconList />}
        action={
          onLayoutChange === undefined ? undefined : (
            <DashboardView
              shown={layout}
              onLayoutChange={onLayoutChange}
              cardColumns={cardColumns}
            />
          )
        }
      >
        <p role={failed ? "alert" : undefined} className="mon-result-count">
          {notice}
        </p>
      </Card>
    </section>
  );
}
