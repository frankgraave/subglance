import { useQuery } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { formatBytes } from "../retention/format";
import { backupKey, fetchBackup } from "./api";

// The setup section, which also documents `subglance restore`.
const backupDocs = "https://github.com/frankgraave/subglance/blob/develop/docs/operations.md#scheduled-backups-to-s3-compatible-storage";

function When({ value }: { value: string }) {
  return <time dateTime={value}>{new Date(value).toLocaleString(undefined, { timeZoneName: "short" })}</time>;
}

/**
 * Scheduled backups, for administrators: the endpoint refuses everyone else,
 * because the target names a bucket.
 *
 * Built on the watchdog card's classes on purpose. Both are process-local
 * history of something SubGlance does on a timer, and both have to say plainly
 * what they do not know rather than draw a lamp claiming present health.
 */
export function BackupCard() {
  const query = useQuery({ queryKey: backupKey, queryFn: ({ signal }) => fetchBackup(signal), refetchInterval: 60_000 });
  const data = query.data;
  return <Card title="Backups" className="watchdog-card">
    <Panel>
      <p className="watchdog-label">Scheduled backups</p>
      {!data ? <p>{query.isError ? "Backup state unavailable." : "Loading backup state…"}</p> : <>
        {query.isError && <p role="status">Backup state unavailable. Showing the last retrieved history.</p>}
        {!data.configured ? <>
          <p>Not configured. The database is not copied anywhere on a schedule.</p>
          <p className="watchdog-note">Set a backup target to keep copies in S3-compatible storage. <a href={backupDocs}>Read about backups</a>.</p>
        </> : <>
          <p>Target <code>{data.target}</code></p>
          {data.last_error !== null && data.last_error_at !== null
            ? <p>The last backup failed at <When value={data.last_error_at} />: {data.last_error}</p>
            : data.last_success_at === null && <p>Waiting for the first backup since this process started.</p>}
          <dl className="watchdog-history">
            <div><dt>Last successful backup</dt><dd>{data.last_success_at === null ? "None since this process started" : <When value={data.last_success_at} />}</dd></div>
            {data.last_object !== null && <div><dt>Object</dt><dd><code>{data.last_object}</code></dd></div>}
            {data.last_size_bytes !== null && <div><dt>Size</dt><dd>{formatBytes(data.last_size_bytes)}</dd></div>}
            <div><dt>Failed runs</dt><dd>{data.failures}</dd></div>
          </dl>
          <p className="watchdog-note">History is held only for this process; the backups themselves stay in the bucket. <a href={backupDocs}>Restoring a backup</a> needs the server stopped.</p>
        </>}
      </>}
    </Panel>
  </Card>;
}
