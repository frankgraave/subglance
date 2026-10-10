import { Suspense, useState } from "react";
import { ErrorBoundary } from "../shell/ErrorBoundary";
import type { AddMonitorProps } from "./AddMonitor";
import type { EditMonitorFormProps } from "./EditMonitorForm";
import type { BulkChannelFormProps } from "./BulkChannelForm";
import { retryableLazy } from "./retryableLazy";
import type { RetryableLazy } from "./retryableLazy";

/*
 * The add and edit monitor forms, loaded when a drawer first asks for one.
 *
 * Nobody needs either form until they press Add or Edit, yet both used to
 * load with the entry bundle, together with everything only they import: the
 * duration and channel pickers, the TLS floor, the JSON assertion and the
 * preview check. The bundle budget had been raised three times for those
 * forms; moving them behind `lazy()` is what buys the room back.
 *
 * Both loaders live in this one file so every caller shares one chunk and
 * one fallback. The fallback is a line of helper text inside the drawer's
 * own panel, the same as the maintenance schedule's: the drawer, its title
 * and its close button are on screen at once, and only the fields arrive a
 * moment later, so the frame does not move when they do.
 *
 * Splitting the forms out added a way to fail that the entry bundle did not
 * have: the chunk can fail to load (a network drop, or a tab left open across
 * an upgrade asking for a file the new binary no longer serves), and a failed
 * `lazy()` throws on render. The shell's add drawer sits outside the screen's
 * boundary, so without one of its own that throw would take the whole page
 * down to the root panel and lose a draft in the other drawer. Each form
 * therefore has its own boundary inside the drawer: the drawer, its title and
 * its close button stay, and the panel offers "Try again", which fetches the
 * chunk afresh, and "Reload the page", for a chunk the server no longer has.
 */

const addMonitorChunk = retryableLazy<AddMonitorProps>(() =>
  import("./AddMonitor").then((module) => ({ default: module.AddMonitor })),
);
const editMonitorFormChunk = retryableLazy<EditMonitorFormProps>(() =>
  import("./EditMonitorForm").then((module) => ({ default: module.EditMonitorForm })),
);

// The selection's channel change: a form few visits open, with its own
// channel read, so it waits for its first use like the two above.
const bulkChannelFormChunk = retryableLazy<BulkChannelFormProps>(() =>
  import("./BulkChannelForm").then((module) => ({ default: module.BulkChannelForm })),
);

function FormLoading() {
  return <p className="field-help">Loading the form…</p>;
}

/**
 * A form from `chunk`: the loading line until it arrives, and a boundary of
 * its own if it does not. The instance sits in state so a retry, which swaps
 * in a fresh one, renders it.
 */
export function ChunkedForm<P extends object>({ chunk, props }: { chunk: RetryableLazy<P>; props: P }) {
  const [Form, setForm] = useState(chunk.current);
  return (
    <ErrorBoundary
      title="The form could not be loaded."
      onRetry={() => {
        // Renewed outside the updater, which React may run twice.
        const fresh = chunk.renew();
        setForm(() => fresh);
      }}
    >
      <Suspense fallback={<FormLoading />}>
        <Form {...props} />
      </Suspense>
    </ErrorBoundary>
  );
}

/** `AddMonitor`, fetched on first use. */
export function LazyAddMonitor(props: AddMonitorProps) {
  return <ChunkedForm chunk={addMonitorChunk} props={props} />;
}

/** `EditMonitorForm`, fetched on first use. */
export function LazyEditMonitorForm(props: EditMonitorFormProps) {
  return <ChunkedForm chunk={editMonitorFormChunk} props={props} />;
}

/** `BulkChannelForm`, fetched on first use. */
export function LazyBulkChannelForm(props: BulkChannelFormProps) {
  return <ChunkedForm chunk={bulkChannelFormChunk} props={props} />;
}
