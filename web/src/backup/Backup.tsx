import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { IconArchive } from "../components/icons";
import { formatBytes } from "../retention/format";
import { backupKey, fetchBackup } from "./api";

// Setup for scheduled backups, and the restore procedure for them.
const backupDocs = "https://github.com/frankgraave/subglance/blob/develop/docs/operations.md#scheduled-backups-to-s3-compatible-storage";
const restoreDocs = "https://github.com/frankgraave/subglance/blob/develop/docs/operations.md#restoring-from-s3";

// The three settings without which no backup runs; Load refuses a target
// that is missing either credential (internal config, validateBackup).
const backupSettings: [string, ReactNode][] = [
  ["SUBGLANCE_BACKUP_TARGET", <>The bucket, and optionally a prefix: <code>s3://bucket/prefix</code></>],
  ["SUBGLANCE_BACKUP_ACCESS_KEY_ID", "The access key ID for that bucket"],
  ["SUBGLANCE_BACKUP_SECRET_ACCESS_KEY_FILE", <>A file holding the secret access key, or the key itself in <code>SUBGLANCE_BACKUP_SECRET_ACCESS_KEY</code></>],
];

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
  return <Card title="Backups" icon={<IconArchive />}>
    <Panel>
      <p className="panel-lead">Scheduled backups</p>
      {!data ? <p>{query.isError ? "Backup state unavailable." : "Loading backup state…"}</p> : <>
        {query.isError && <p role="status">Backup state unavailable. Showing the last retrieved history.</p>}
        {!data.configured ? <>
          <p>Not configured. SubGlance has no scheduled backup target.</p>
          {/* Names the settings rather than only linking to them: the card is
              the one place an operator looks, and a link alone sends them off
              to find three variable names. They are environment variables
              because the credentials can be nothing else (a flag shows in ps). */}
          <p className="panel-note">Scheduled backups to S3-compatible storage need these environment variables, then a restart:</p>
          <dl className="panel-settings">
            {backupSettings.map(([name, purpose]) => <div key={name}><dt><code>{name}</code></dt><dd>{purpose}</dd></div>)}
          </dl>
          <p className="panel-note">
            Set <code>SUBGLANCE_BACKUP_REGION</code> when the bucket is not in <code>us-east-1</code>, and <code>SUBGLANCE_BACKUP_ENDPOINT</code> for
            storage other than AWS S3. <a href={backupDocs}>Read about backups</a>.
          </p>
        </> : <>
          <p>Target <code>{data.target}</code></p>
          {data.last_error !== null && data.last_error_at !== null
            ? <p>The last backup run reported an error at <span className="face-mono"><When value={data.last_error_at} /></span>: {data.last_error}</p>
            : data.last_success_at === null && <p>Waiting for the first backup since this process started.</p>}
          <dl className="panel-facts">
            <div><dt>Last successful backup</dt><dd>{data.last_success_at === null ? "None since this process started" : <When value={data.last_success_at} />}</dd></div>
            {data.last_object !== null && <div><dt>Object</dt><dd><code>{data.last_object}</code></dd></div>}
            {data.last_size_bytes !== null && <div><dt>Size</dt><dd className="face-mono">{formatBytes(data.last_size_bytes)}</dd></div>}
            <div><dt>Failed runs</dt><dd className="face-mono">{data.failures}</dd></div>
          </dl>
          <p className="panel-note">History is held only for this process; the backups themselves stay in the bucket. <a href={restoreDocs}>Restoring a backup</a> needs the server stopped.</p>
        </>}
      </>}
    </Panel>
  </Card>;
}
