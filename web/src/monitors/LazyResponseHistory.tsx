import { Suspense, useState } from "react";
import { Card, Panel } from "../components/Card";
import { IconResponse } from "../components/icons";
import { ErrorBoundary } from "../shell/ErrorBoundary";
import type { ResponseHistoryProps } from "./ResponseHistory";
import { retryableLazy } from "./retryableLazy";

/*
 * The failure responses card, loaded after the rest of the detail page.
 *
 * It is the last card on the page (SUB-184): the raw checks behind the
 * summaries above it, read once those have said what happened. Nothing above
 * it waits for it, so it does not need to arrive with the app — the same
 * reasoning that took the retention card out of the entry chunk rather than
 * raising the budget for every visitor.
 *
 * It has a boundary of its own, like the monitor forms. A chunk can fail to
 * load (a tab left open across an upgrade asks for a file the new binary no
 * longer serves), and this page is where an alert link lands: a failed
 * diagnostics card must not take the status, uptime and incidents above it
 * down with it. "Try again" fetches the chunk afresh.
 */
const chunk = retryableLazy<ResponseHistoryProps>(() =>
  import("./ResponseHistory").then((module) => ({ default: module.ResponseHistory })),
);

/** The same card and glyph as the one it stands in for (DESIGN.md §8.2). */
function Loading() {
  return (
    <Card title="Failure responses" icon={<IconResponse />} headingLevel={2}>
      <Panel>
        <p className="mon-detail-note">Loading failure responses…</p>
      </Panel>
    </Card>
  );
}

export function LazyResponseHistory(props: ResponseHistoryProps) {
  const [History, setHistory] = useState(chunk.current);
  return (
    <ErrorBoundary
      title="The failure responses could not be loaded."
      onRetry={() => {
        // Renewed outside the updater, which React may run twice.
        const fresh = chunk.renew();
        setHistory(() => fresh);
      }}
    >
      <Suspense fallback={<Loading />}>
        <History {...props} />
      </Suspense>
    </ErrorBoundary>
  );
}
