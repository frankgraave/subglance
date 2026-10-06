import { useId, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { Checkbox } from "../components/Choice";
import { IconAlert, IconNetwork } from "../components/icons";
import { ApiError } from "../api/http";
import {
  connectivitySettingsKey, fetchConnectivitySettings, parseTargets, saveConnectivitySettings,
  type ConnectivityChange, type ConnectivitySettings,
} from "./settings";
import { connectivityKey } from "./api";

type Outcome = "saved" | "stale" | null;

/** The server's refusal, under the field it names or under the form. */
function Refusal({ id, children }: { id?: string; children: ReactNode }) {
  return <p className="field-error connectivity-error" role="alert" id={id}><IconAlert /><span>{children}</span></p>;
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((item, i) => item === b[i]);

function ConnectivityForm({ data, outcome, setOutcome }: {
  data: ConnectivitySettings; outcome: Outcome; setOutcome: (outcome: Outcome) => void;
}) {
  const client = useQueryClient();
  const id = useId();
  const [enabled, setEnabled] = useState(data.enabled.value);
  const [text, setText] = useState(data.targets.value.join("\n"));
  const [saving, setSaving] = useState(false);
  const [rejection, setRejection] = useState<{ field?: string; message: string } | null>(null);

  const enabledPinned = data.enabled.source === "pinned";
  const targetsPinned = data.targets.source === "pinned";
  const targets = parseTargets(text);
  const targetsChanged = !sameList(targets, data.targets.value);
  const changed = enabled !== data.enabled.value || targetsChanged;
  const isDefault = sameList(targets, data.default_targets);
  const edit = <T,>(set: (value: T) => void) => (value: T) => { set(value); setOutcome(null); setRejection(null); };
  const editText = edit(setText);
  const editEnabled = edit(setEnabled);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!changed || saving) return;
    // Without a version the save could only be unconditional, which is the
    // overwrite the version exists to prevent. Refuse rather than downgrade.
    if (!data.etag) {
      setRejection({ message: "Reload the page before saving: the server did not say which version of the settings this is." });
      return;
    }
    // Only what is not pinned and has changed: a pinned field is refused
    // with a 409, and an unchanged list need not be checked again.
    const body: ConnectivityChange = {};
    if (!enabledPinned && enabled !== data.enabled.value) body.enabled = enabled;
    if (!targetsPinned && targetsChanged) body.targets = targets;
    setSaving(true);
    setRejection(null);
    setOutcome(null);
    try {
      client.setQueryData(connectivitySettingsKey, await saveConnectivitySettings(body, data.etag));
      // The dashboard's "no outbound connection" line reads the state the
      // change just reset; it is asked again rather than left saying it.
      void client.invalidateQueries({ queryKey: connectivityKey });
      setOutcome("saved");
    } catch (error) {
      // Someone saved since this page read the settings. The draft was made
      // against settings no longer in force, so it is not retried: the form
      // restarts from what the server now says.
      if (error instanceof ApiError && error.status === 412) {
        setOutcome("stale");
        void client.invalidateQueries({ queryKey: connectivitySettingsKey });
        return;
      }
      setRejection(error instanceof ApiError
        ? { message: error.message, field: error.field ?? undefined }
        : { message: "Could not reach SubGlance. Check your connection and try again." });
    } finally {
      setSaving(false);
    }
  }

  const enabledError = rejection?.field === "enabled" ? rejection.message : undefined;
  const targetsError = rejection?.field === "targets" ? rejection.message : undefined;
  const describe = (help: string, error?: string) => error ? `${help} ${help}-error` : help;
  return (
    <form className="stack" aria-label="Connectivity check" onSubmit={submit}>
      <div className="field">
        <Checkbox checked={enabled} disabled={enabledPinned || saving}
          aria-describedby={describe(`${id}-enabled-help`, enabledError)} aria-invalid={enabledError ? true : undefined}
          onChange={(event) => editEnabled(event.target.checked)}>
          Check this host's own connection before confirming an outage
        </Checkbox>
        <p className="panel-note" id={`${id}-enabled-help`}>
          {enabledPinned ? `Set by ${data.enabled.pinned_by} to ${data.enabled.value ? "on" : "off"}; change it there.`
            : enabled ? "When every address below fails as well, the failure is this host's: it is kept as a warning, and no incident or alert follows."
            : "Off: when this host loses its connection, every monitor is reported down and alerts once the connection is back."}
        </p>
        {enabledError && <Refusal id={`${id}-enabled-help-error`}>{enabledError}</Refusal>}
      </div>
      <div className="field">
        <label className="field-label" htmlFor={`${id}-targets`}>Addresses to dial</label>
        <textarea className="input input--inset input--code" id={`${id}-targets`} rows={3}
          spellCheck={false} autoComplete="off" autoCapitalize="off" value={text}
          disabled={targetsPinned || saving} aria-invalid={targetsError ? true : undefined}
          aria-describedby={describe(`${id}-targets-help`, targetsError)}
          onChange={(event) => editText(event.target.value)} />
        <p className="panel-note" id={`${id}-targets-help`}>
          {targetsPinned ? `Set by ${data.targets.pinned_by}; change it there.`
            : `One host:port per line, at most ${data.max_targets}. Choose addresses that do not share a failure with your monitors, such as your gateway. Saving new addresses ends an ongoing offline spell without a notice.`}
        </p>
        {targetsError && <Refusal id={`${id}-targets-help-error`}>{targetsError}</Refusal>}
        {!targetsPinned && (
          <div>
            <button className="button button--compact" type="button" disabled={isDefault || saving}
              onClick={() => editText(data.default_targets.join("\n"))}>Restore default addresses</button>
          </div>
        )}
      </div>
      {rejection && !rejection.field && <Refusal>{rejection.message}</Refusal>}
      {enabledPinned && targetsPinned
        ? <p className="panel-note">Both settings are fixed by the server's configuration.</p>
        : <div>
          <button className="button-solid" type="submit" disabled={!changed || saving}>
            {saving ? "Saving…" : "Save connectivity check"}
          </button>
        </div>}
      {outcome && <p role={outcome === "stale" ? "alert" : "status"}>{outcome === "stale"
        ? "The connectivity check was changed by someone else while you were editing. The form now shows what is in force; check it and save again."
        : "Saved. The check uses these settings now."}</p>}
    </form>
  );
}

/**
 * The connectivity check (SUB-168): whether SubGlance tests this host's own
 * connection before confirming an outage, and which addresses it dials.
 * Administrators only, like the endpoint: an address can name a host on the
 * operator's own network.
 */
export function ConnectivityCard() {
  const query = useQuery({ queryKey: connectivitySettingsKey, queryFn: ({ signal }) => fetchConnectivitySettings(signal) });
  // Held here rather than in the form: a save, or a reload after a refused
  // one, remounts the form (see its key), and the message has to outlive it.
  const [outcome, setOutcome] = useState<Outcome>(null);
  const data = query.data;
  return (
    <Card title="Connectivity check" icon={<IconNetwork />}>
      <Panel spacing="form">
        {/* The same words as docs/operations.md: this is the one setting that
            makes the instance send traffic to hosts nobody configured as a
            monitor, so the card says so before it asks anything. */}
        <p className="panel-note">
          Before a failure on a network error confirms an outage, SubGlance dials these addresses over TCP to tell a
          down monitor from a down uplink. This is outbound traffic: it is sent only when an outage is about to be
          confirmed, never on a timer while everything is healthy, and it only completes a handshake.
        </p>
        {!data ? <p>{query.isError ? "Connectivity settings unavailable." : "Loading connectivity settings…"}</p>
          : <ConnectivityForm key={[data.enabled.value, ...data.targets.value].join("\n")}
            data={data} outcome={outcome} setOutcome={setOutcome} />}
      </Panel>
    </Card>
  );
}
