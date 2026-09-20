import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import { IconAlert } from "../components/icons";
import { ApiError, changePassword } from "./api";
import type { Rejection } from "./CredentialsForm";
import { passwordStrength, describeStrength } from "./password";

const EMPTY = { current_password: "", new_password: "", confirmation: "" };
const FIELDS = [
  ["current_password", "Current password"],
  ["new_password", "New password"],
  ["confirmation", "Confirm new password"],
] as const;

function PasswordError({ id, children }: { id?: string; children: ReactNode }) {
  return <p className="auth-error auth-password-error" role="alert" id={id}>
    <IconAlert /><span>{children}</span>
  </p>;
}

/** Passwords live only in this mounted form, never in a cache or storage. */
export function ChangePassword() {
  const id = useId();
  const [values, setValues] = useState(EMPTY);
  const [rejection, setRejection] = useState<Rejection | null>(null);
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const pending = useRef<AbortController | null>(null);
  useEffect(() => () => pending.current?.abort(), []);
  const incomplete = !values.current_password || !values.new_password || !values.confirmation ||
    passwordStrength(values.new_password) === "short";

  async function submit(event: FormEvent) {
    event.preventDefault();
    // The ref also locks two submissions dispatched before React commits.
    if (pending.current || incomplete) return;
    if (values.new_password !== values.confirmation) {
      setRejection({ field: "confirmation", message: "The new passwords do not match." });
      return;
    }
    const controller = new AbortController();
    pending.current = controller;
    setSaving(true);
    setRejection(null);
    setSaved(false);
    try {
      await changePassword(values.current_password, values.new_password, controller.signal);
      if (controller.signal.aborted) return;
      setValues(EMPTY);
      setSaved(true);
    } catch (error) {
      if (controller.signal.aborted) return;
      setRejection(error instanceof ApiError
        ? { message: error.message, field: error.field ?? undefined }
        : { message: "Could not reach SubGlance. Check your connection and try again." });
    } finally {
      if (!controller.signal.aborted) {
        pending.current = null;
        setSaving(false);
      }
    }
  }

  return (
    <form className="auth-form" aria-label="Change password" onSubmit={submit}>
      <p className="auth-intro">Changing your password signs out every other session.</p>
      {FIELDS.map(([field, label]) => (
        <div className="auth-field" key={field}>
          <label className="auth-label" htmlFor={`${id}-${field}`}>{label}</label>
          <input
            className="auth-input" id={`${id}-${field}`} name={field} type="password"
            value={values[field]} required disabled={saving}
            autoComplete={field === "current_password" ? "current-password" : "new-password"}
            onChange={(event) => {
              setValues({ ...values, [field]: event.target.value });
              setSaved(false);
              if (rejection?.field === field) setRejection(null);
            }}
            aria-invalid={rejection?.field === field || undefined}
            aria-describedby={[
              rejection?.field === field ? `${id}-error` : "",
              field === "new_password" ? `${id}-strength` : "",
            ].filter(Boolean).join(" ") || undefined}
          />
          {field === "new_password" && (
            <p className="auth-intro" id={`${id}-strength`}>{describeStrength(values.new_password)}</p>
          )}
          {rejection?.field === field && (
            <PasswordError id={`${id}-error`}>{rejection.message}</PasswordError>
          )}
        </div>
      ))}
      {rejection && !FIELDS.some(([field]) => field === rejection.field) && (
        <PasswordError>{rejection.message}</PasswordError>
      )}
      <div>
        <button className="auth-submit" disabled={saving || incomplete} type="submit">
          {saving ? "Changing password…" : "Change password"}
        </button>
      </div>
      {saved && <p role="status">Password changed. Every other session was signed out. You are still signed in on this tab.</p>}
    </form>
  );
}
