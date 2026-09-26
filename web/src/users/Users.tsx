import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { StateChip } from "../components/Chip";
import { ApiError, MIN_PASSWORD_LENGTH } from "../auth/api";
import { ROLES, createUser, deleteUser, fetchUsers, setUserRole, usersKey, type Account, type UserRole } from "./api";

const ROLE_HELP: Record<UserRole, string> = {
  viewer: "Viewer: sees monitors, incidents and settings, and changes nothing. Right for someone who only needs to look.",
  editor: "Editor: also creates and changes monitors, channels and maintenance, and acknowledges incidents.",
  admin: "Admin: also manages accounts and instance-wide settings such as retention.",
};

const label = (role: UserRole) => role[0].toUpperCase() + role.slice(1);
const withArticle = (role: UserRole) => `${role === "viewer" ? "a" : "an"} ${role}`;
const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });

type Rejection = { field?: string; message: string };
const rejectionOf = (error: unknown): Rejection => error instanceof ApiError
  ? { field: error.field ?? undefined, message: error.message }
  : { message: error instanceof Error ? error.message : "Could not reach SubGlance. Check your connection and try again." };

/**
 * Add an account with a password the administrator sets.
 *
 * There is no invite flow: that needs mail delivery, an expiring link and a
 * public accept route, none of which a self-hosted instance has. The password
 * is handed over out of band and its owner changes it under Account.
 */
function CreateForm({ onCreated, onCancel }: { onCreated: (account: Account) => void; onCancel: () => void }) {
  const id = useId();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  // Least privilege by default: giving more has to be a choice.
  const [role, setRole] = useState<UserRole>("viewer");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<Rejection | null>(null);
  const ready = email.trim() !== "" && password.length >= MIN_PASSWORD_LENGTH;
  const fieldError = (field: string) => error?.field === field ? `${id}-error` : undefined;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!ready || saving) return;
    setSaving(true);
    setError(null);
    try {
      onCreated(await createUser({ email: email.trim(), password, role }));
    } catch (err) {
      setError(rejectionOf(err));
      setSaving(false);
    }
  }

  return (
    <form className="auth-form" aria-label="Add user" onSubmit={submit}>
      <div className="retention-inputs">
        <div className="auth-field">
          <label className="auth-label" htmlFor={`${id}-email`}>Email</label>
          <input className="auth-input" id={`${id}-email`} type="email" value={email} autoComplete="off" spellCheck={false}
            aria-describedby={fieldError("email")} aria-invalid={fieldError("email") ? true : undefined}
            onChange={(event) => setEmail(event.target.value)} />
        </div>
        <div className="auth-field">
          <label className="auth-label" htmlFor={`${id}-password`}>Password</label>
          {/* new-password, so a browser offers to generate one rather than
              filling in the administrator's own. */}
          <input className="auth-input" id={`${id}-password`} type="password" value={password} autoComplete="new-password"
            aria-describedby={[`${id}-password-help`, fieldError("password")].filter(Boolean).join(" ")}
            aria-invalid={fieldError("password") ? true : undefined}
            onChange={(event) => setPassword(event.target.value)} />
        </div>
        <div className="auth-field">
          <label className="auth-label" htmlFor={`${id}-role`}>Role</label>
          <select className="auth-input" id={`${id}-role`} value={role} aria-describedby={`${id}-role-help`}
            onChange={(event) => setRole(event.target.value as UserRole)}>
            {ROLES.map((item) => <option key={item} value={item}>{label(item)}</option>)}
          </select>
        </div>
      </div>
      <p className="retention-note" id={`${id}-password-help`}>
        At least {MIN_PASSWORD_LENGTH} characters. Hand it over yourself; the new user can change it under Account.
      </p>
      <p className="retention-note" id={`${id}-role-help`}>{ROLE_HELP[role]}</p>
      {error && <p className="auth-error" role="alert" id={`${id}-error`}>{error.message}</p>}
      <div className="add-actions">
        <button className="auth-submit" type="submit" disabled={!ready || saving}>{saving ? "Adding…" : "Add user"}</button>
        <button className="add-button" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}

function UserRow({ account, you, onChanged }: { account: Account; you: boolean; onChanged: (message: string) => void }) {
  const [role, setRole] = useState<UserRole>(account.role);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function run(action: () => Promise<string>) {
    setBusy(true);
    setError(null);
    try {
      onChanged(await action());
    } catch (err) {
      setError(rejectionOf(err).message);
      setBusy(false);
    }
  }

  const saveRole = () => run(async () => {
    const next = await setUserRole(account.id, role);
    return `${next.email} is now ${withArticle(next.role)}.`;
  });
  const remove = () => run(async () => {
    await deleteUser(account.id);
    return `${account.email} was removed.`;
  });

  // Removing and re-roling are neutral buttons: --down text on a dark panel
  // measures 3.9:1, under the 4.5:1 a label needs. The second step guards it.
  return (
    <li className="retention-inputs">
      <div className="inv-main">
        <p><strong>{account.email}</strong></p>
        <p className="retention-note">created {day(account.created_at)}</p>
        {error && <p className="auth-error" role="alert">{error}</p>}
      </div>
      {you ? <>
        {/* No role picker and no Remove on your own row: the one administrator
            on the page is the one person who could not undo either. */}
        <StateChip>{account.role}</StateChip>
        <StateChip>you</StateChip>
      </> : <>
        <select className="auth-input" aria-label={`Role for ${account.email}`} value={role} disabled={busy}
          onChange={(event) => { setRole(event.target.value as UserRole); setError(null); }}>
          {ROLES.map((item) => <option key={item} value={item}>{label(item)}</option>)}
        </select>
        {/* A change is saved by a button, not by the select itself: arrowing
            through a closed select fires a change per option on some
            platforms, which would re-role the account at every keypress. */}
        {role !== account.role && (
          <span className="add-actions">
            <button type="button" className="add-button add-button-primary" disabled={busy} onClick={() => void saveRole()}
              aria-label={`Save role for ${account.email}`}>Save role</button>
            <button type="button" className="add-button" disabled={busy} onClick={() => setRole(account.role)}>Undo</button>
          </span>
        )}
        {role === account.role && (confirming ? (
          <span className="add-actions">
            <button type="button" className="add-button" disabled={busy} onClick={() => void remove()}
              aria-label={`Confirm removing ${account.email}`}>Remove now</button>
            <button type="button" className="add-button" disabled={busy} onClick={() => setConfirming(false)}>Keep</button>
          </span>
        ) : (
          <button type="button" className="add-button" aria-label={`Remove ${account.email}`} onClick={() => setConfirming(true)}>Remove</button>
        ))}
      </>}
      {confirming && role === account.role && (
        <p className="retention-note">Removing signs {account.email} out everywhere and revokes their API tokens.</p>
      )}
    </li>
  );
}

/**
 * Accounts on this instance: add one, change its role, remove it.
 *
 * Administrators only, because the list itself is admin-only on the server.
 * Deliberately small: three flat roles, no groups, no per-monitor rights, no
 * invites. The server keeps the last administrator whatever the page does.
 */
export function UsersCard({ userId }: { userId?: number }) {
  const client = useQueryClient();
  const query = useQuery({ queryKey: usersKey, queryFn: ({ signal }) => fetchUsers(signal) });
  const [adding, setAdding] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const users = query.data;
  const done = (text: string) => {
    setMessage(text);
    void client.invalidateQueries({ queryKey: usersKey });
  };

  return (
    <Card title="Users" className="retention-card" note={users ? `${users.length} ${users.length === 1 ? "account" : "accounts"}` : undefined}
      action={!adding && <button type="button" className="add-button" onClick={() => { setAdding(true); setMessage(null); }}>Add user</button>}>
      <Panel>
        {adding && <CreateForm onCancel={() => setAdding(false)}
          onCreated={(account) => { setAdding(false); done(`${account.email} was added as ${withArticle(account.role)}.`); }} />}
        <div role="status" aria-live="polite" className="add-result-region">{message && <p className="add-result">{message}</p>}</div>
        {!users ? <p>{query.isError ? "Users unavailable." : "Loading users…"}</p>
          : <ul className="auth-form" aria-label="Accounts">
            {/* Keyed on the role too, so a saved change restarts the row's draft from the server's answer. */}
            {users.map((account) => <UserRow key={`${account.id}/${account.role}`} account={account} you={account.id === userId} onChanged={done} />)}
          </ul>}
      </Panel>
    </Card>
  );
}
