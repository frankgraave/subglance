import { useId, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card, Panel } from "../components/Card";
import { StateChip } from "../components/Chip";
import { ConfirmDelete } from "../components/ConfirmDelete";
import { RoleChoice } from "../components/RoleChoice";
import { IconTrash, IconUsers } from "../components/icons";
import { ApiError, MIN_PASSWORD_LENGTH } from "../auth/api";
import { createUser, deleteUser, fetchUsers, setUserRole, usersKey, type Account, type UserRole } from "./api";

const ROLE_HELP: Record<UserRole, string> = {
  viewer: "Viewer: sees monitors, incidents and settings, and changes nothing. Right for someone who only needs to look.",
  editor: "Editor: also creates and changes monitors, channels and maintenance, and mutes repeat alerts on incidents.",
  admin: "Admin: also manages accounts and instance-wide settings such as retention.",
};

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
    <form className="stack" aria-label="Add user" onSubmit={submit}>
      <div className="control-row">
        <div className="field">
          <label className="field-label" htmlFor={`${id}-email`}>Email</label>
          <input className="input input--inset" id={`${id}-email`} type="email" value={email} autoComplete="off" spellCheck={false}
            aria-describedby={fieldError("email")} aria-invalid={fieldError("email") ? true : undefined}
            onChange={(event) => setEmail(event.target.value)} />
        </div>
        <div className="field">
          <label className="field-label" htmlFor={`${id}-password`}>Password</label>
          {/* new-password, so a browser offers to generate one rather than
              filling in the administrator's own. */}
          <input className="input input--inset" id={`${id}-password`} type="password" value={password} autoComplete="new-password"
            aria-describedby={[`${id}-password-help`, fieldError("password")].filter(Boolean).join(" ")}
            aria-invalid={fieldError("password") ? true : undefined}
            onChange={(event) => setPassword(event.target.value)} />
        </div>
        <div className="field">
          {/* The group carries the name "Role"; this is its visible caption. */}
          <span className="field-label" aria-hidden="true">Role</span>
          <RoleChoice label="Role" value={role} onChange={setRole} describedBy={`${id}-role-help`} />
        </div>
      </div>
      <p className="panel-note" id={`${id}-password-help`}>
        At least {MIN_PASSWORD_LENGTH} characters. Hand it over yourself; the new user can change it under Account.
      </p>
      <p className="panel-note" id={`${id}-role-help`}>{ROLE_HELP[role]}</p>
      {error && <p className="field-error" role="alert" id={`${id}-error`}>{error.message}</p>}
      <div className="button-row">
        <button className="button-solid" type="submit" disabled={!ready || saving}>{saving ? "Adding…" : "Add user"}</button>
        <button className="button" type="button" onClick={onCancel}>Cancel</button>
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

  // Re-roling is a pair of neutral buttons. Removing is the bin every other
  // list row uses (monitors, channels): red ink on a glyph, which needs 3:1
  // as a graphic, where --down on a word measures 3.9:1 on a dark panel and a
  // label needs 4.5:1. The retyped address in ConfirmDelete guards it.
  return (
    <li className="control-row">
      <div className="control-row-main">
        <p><strong>{account.email}</strong></p>
        <p className="panel-note">created {day(account.created_at)}</p>
        {error && <p className="field-error" role="alert">{error}</p>}
      </div>
      {you ? <>
        {/* No role picker and no Remove on your own row: the one administrator
            on the page is the one person who could not undo either. */}
        <StateChip>{account.role}</StateChip>
        <StateChip>you</StateChip>
      </> : <>
        <RoleChoice label={`Role for ${account.email}`} value={role} disabled={busy}
          onChange={(next) => { setRole(next); setError(null); }} />
        {/* A change is saved by a button, not by pressing a segment: what an
            account may do should not change on a press that missed its
            neighbour, so a pressed segment is a draft until Save role. */}
        {role !== account.role ? (
          <span className="button-row">
            <button type="button" className="button button--primary" disabled={busy} onClick={() => void saveRole()}
              aria-label={`Save role for ${account.email}`}>Save role</button>
            <button type="button" className="button" disabled={busy} onClick={() => setRole(account.role)}>Undo</button>
          </span>
        ) : (
          <button type="button" className="icon-button button--danger" disabled={busy}
            aria-label={`Remove ${account.email}`} title={`Remove ${account.email}`} onClick={() => setConfirming(true)}>
            <IconTrash />
          </button>
        )}
      </>}
      {/* The address is retyped, not clicked through (DESIGN.md §7.5):
          removal signs the account out and revokes its tokens, and two
          similar addresses are a real way to remove the wrong one. */}
      {confirming && (
        <ConfirmDelete open onClose={() => setConfirming(false)} kind="account" name={account.email}
          consequence={`${account.email} is removed, signed out everywhere, and its API tokens are revoked. This cannot be undone.`}
          onConfirm={() => { setConfirming(false); void remove(); }} />
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
    // Titled with its count like every card that frames a list (DESIGN.md §8.3);
    // the count is read from the rows it renders, so the two cannot drift.
    <Card title={users ? `Users (${users.length})` : "Users"} icon={<IconUsers />}
      action={!adding && <button type="button" className="button" onClick={() => { setAdding(true); setMessage(null); }}>Add user</button>}>
      <Panel spacing="form">
        {adding && <CreateForm onCancel={() => setAdding(false)}
          onCreated={(account) => { setAdding(false); done(`${account.email} was added as ${withArticle(account.role)}.`); }} />}
        <div role="status" aria-live="polite" className="result-region">{message && <p className="result">{message}</p>}</div>
        {!users ? <p>{query.isError ? "Users unavailable." : "Loading users…"}</p>
          : <ul className="stack" aria-label="Accounts">
            {/* Keyed on the role too, so a saved change restarts the row's draft from the server's answer. */}
            {users.map((account) => <UserRow key={`${account.id}/${account.role}`} account={account} you={account.id === userId} onChanged={done} />)}
          </ul>}
      </Panel>
    </Card>
  );
}
