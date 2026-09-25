import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { Value } from "../components/Value";
import { useNow } from "../live/useNow";
import { diagnosticsKey, fetchDiagnostics } from "./api";
import { diagnosticsText, formatBytes, formatUptime } from "./format";

/**
 * The instance card: what is running, on what database, and whether the check
 * pipeline keeps up.
 *
 * The number it is designed around is the worker pool. A monitor that falls
 * behind its own schedule is the failure nobody sees, because the dashboard
 * stays green while stale checks simply stop updating. Busy against total,
 * with the queue beside it, shows that before it becomes "why did it not
 * alert me". A zero queue dims, because zero is the good reading; a queue,
 * a skipped check or a failed write carries the warning mark, which does not
 * depend on colour.
 */

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return <div><dt>{label}</dt><dd>{children}</dd></div>;
}

function Count({ n, warning }: { n: number; warning?: string }) {
  return <Value value={n} {...(n > 0 && warning ? { warning } : {})}>{n.toLocaleString()}</Value>;
}

export function DiagnosticsCard() {
  const query = useQuery({ queryKey: diagnosticsKey, queryFn: ({ signal }) => fetchDiagnostics(signal), refetchInterval: 15_000 });
  const now = useNow();
  const [copied, setCopied] = useState<string | null>(null);
  const d = query.data;
  const old = d !== undefined && (query.isError || now - query.dataUpdatedAt > 45_000);

  const copy = () => {
    if (!d) return;
    // navigator.clipboard is absent over plain HTTP on a LAN address, which is
    // how a self-hosted instance is often reached. Say so rather than fail.
    if (navigator.clipboard === undefined) { setCopied("Clipboard unavailable on this connection; select the values instead."); return; }
    void navigator.clipboard.writeText(diagnosticsText(d)).then(
      () => setCopied("Copied. The database path is left out."),
      () => setCopied("Could not copy; select the values instead."),
    );
  };

  const s = d?.scheduler;
  return <Card title="Instance" className="diag-card"
    action={d && <button type="button" className="add-button" onClick={copy}>Copy diagnostics</button>}>
    {!d ? <Panel><p>{query.isError ? "Diagnostics unavailable." : "Loading diagnostics…"}</p></Panel> : <>
      {old && <p role="status">Diagnostics unavailable. Showing the last readings.</p>}
      <p role="status" className="diag-note">{copied}</p>
      <div className="diag-grid">
        <Panel label="Build"><dl>
          <Row label="Version">{d.version}{d.commit && ` (${d.commit})`}</Row>
          <Row label="Runtime">{d.go_version} {d.platform}</Row>
        </dl></Panel>
        <Panel label="Process"><dl>
          <Row label="Uptime">{formatUptime(d.uptime_seconds)}</Row>
          <Row label="Started"><time dateTime={d.started_at}>{new Date(d.started_at).toLocaleString(undefined, { timeZoneName: "short" })}</time></Row>
        </dl></Panel>
        <Panel label="Database"><dl>
          <Row label="Path"><code>{d.database.path}</code></Row>
          <Row label="Size"><Value value={d.database.bytes}>{formatBytes(d.database.bytes)}</Value>{" "}
            + <Value value={d.database.wal_bytes}>{formatBytes(d.database.wal_bytes)}</Value> write-ahead log</Row>
          <Row label="Journal mode">
            <Value {...(d.database.journal_mode === "wal" ? {} : { warning: "not WAL: readers and the writer block each other" })}>{d.database.journal_mode.toUpperCase()}</Value>
          </Row>
        </dl></Panel>
        <Panel label="Checks">{!s ? <p>No check pipeline is attached to this process.</p> : <dl>
          <Row label="Worker pool"><Value value={s.busy}>{`${s.busy} / ${s.workers} busy`}</Value></Row>
          <Row label="Queue depth"><Count n={s.queue_depth} warning="checks are waiting for a free worker" /></Row>
          <Row label="Skipped since start"><Count n={s.skipped_checks} warning="a check outran its interval; the pool may be behind" /></Row>
          <Row label="Failed writes"><Count n={s.heartbeat_write_failures} warning="heartbeats could not be written; check free disk space" /></Row>
          <Row label="Monitors scheduled"><Count n={s.scheduled} /></Row>
        </dl>}</Panel>
      </div>
    </>}
  </Card>;
}
