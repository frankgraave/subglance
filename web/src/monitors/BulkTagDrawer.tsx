import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { Drawer } from "../components/Drawer";
import { Panel } from "../components/Card";
import type { TagOperation, TagResult } from "./bulkTagsApi";

export type TagChange = (
  operation: TagOperation,
  etag?: string,
) => Promise<TagResult>;

/** Preview is a separate decision from commit. No per-monitor write fan-out. */
export function BulkTagDrawer({
  selectedIds,
  onChange,
  onClose,
}: {
  selectedIds: readonly string[];
  onChange: TagChange;
  onClose: () => void;
}) {
  const [action, setAction] = useState<TagOperation["action"]>("apply");
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [pending, setPending] = useState(false);
  const busy = useRef(false);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const resultRef = useRef<HTMLParagraphElement>(null);
  const errorRef = useRef<HTMLParagraphElement>(null);
  const workingRef = useRef<HTMLParagraphElement>(null);
  const previewRef = useRef<HTMLParagraphElement>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [preview, setPreview] = useState<{
    signature: string;
    result: TagResult;
  } | null>(null);
  const [saved, setSaved] = useState<TagResult | null>(null);
  const global = action === "rename_key" || action === "rename_value";
  const operation: TagOperation =
    action === "rename_key"
      ? {
          action,
          key: key.trim().toLowerCase(),
          new_key: newKey.trim().toLowerCase(),
        }
      : action === "rename_value"
        ? {
            action,
            key: key.trim().toLowerCase(),
            value: value.trim(),
            new_value: newValue.trim(),
          }
        : {
            action,
            monitor_ids: selectedIds.map(Number),
            key: key.trim().toLowerCase(),
            value: value.trim(),
          };
  const signature = JSON.stringify(operation);
  const currentPreview =
    preview?.signature === signature ? preview.result : null;
  // Disabling a fieldset or removing Confirm drops browser focus to the page.
  // Keep keyboard users inside the dialog when an async operation settles.
  useEffect(() => {
    if (pending) {
      workingRef.current?.focus();
      return;
    }
    if (problem) errorRef.current?.focus();
    else if (saved) resultRef.current?.focus();
    else if (currentPreview?.changed === 0) previewRef.current?.focus();
    else if (currentPreview) confirmRef.current?.focus();
  }, [pending, problem, saved, currentPreview]);
  const reset = () => {
    setPreview(null);
    setSaved(null);
    setProblem(null);
  };
  const run = async (commit: boolean) => {
    if (busy.current) return;
    busy.current = true;
    setPending(true);
    setProblem(null);
    try {
      const result = await onChange(
        operation,
        commit ? currentPreview?.etag : undefined,
      );
      if (commit) {
        setSaved(result);
        setPreview(null);
      } else {
        setPreview({ signature, result });
        setSaved(null);
      }
    } catch (error) {
      setPreview(null);
      setProblem(
        error instanceof Error
          ? error.message
          : "The tag change could not be saved.",
      );
    } finally {
      busy.current = false;
      setPending(false);
    }
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    void run(false);
  };
  return (
    <Drawer
      open
      title="Manage tags"
      onClose={() => {
        if (!busy.current) onClose();
      }}
      className="bulk-tags-drawer"
    >
      <Panel>
        <form className="add-form bulk-tags-form" onSubmit={submit}>
          <p>
            {global
              ? "Every matching monitor in this instance, not just the selection or visible list."
              : `${selectedIds.length} selected monitors, including any hidden by filters.`}
          </p>
          <fieldset className="add-fieldset" disabled={pending}>
            <legend className="add-legend">Tag change</legend>
            <label className="add-field">
              <span className="add-label">Action</span>
              <select
                className="add-input"
                value={action}
                onChange={(e) => {
                  reset();
                  setAction(e.target.value as typeof action);
                }}
              >
                <option value="apply">Apply to selected monitors</option>
                <option value="remove">Remove from selected monitors</option>
                <option value="rename_key">Rename key across instance</option>
                <option value="rename_value">
                  Rename value across instance
                </option>
              </select>
            </label>
            <label className="add-field">
              <span className="add-label">Tag key</span>
              <input
                className="add-input"
                value={key}
                required
                maxLength={32}
                onChange={(e) => {
                  reset();
                  setKey(e.target.value);
                }}
              />
            </label>
            {action !== "rename_key" && (
              <label className="add-field">
                <span className="add-label">Tag value</span>
                <input
                  className="add-input"
                  value={value}
                  required
                  onChange={(e) => {
                    reset();
                    setValue(e.target.value);
                  }}
                />
              </label>
            )}
            {action === "rename_key" && (
              <label className="add-field">
                <span className="add-label">New key</span>
                <input
                  className="add-input"
                  value={newKey}
                  required
                  maxLength={32}
                  onChange={(e) => {
                    reset();
                    setNewKey(e.target.value);
                  }}
                />
              </label>
            )}
            {action === "rename_value" && (
              <label className="add-field">
                <span className="add-label">New value</span>
                <input
                  className="add-input"
                  value={newValue}
                  required
                  onChange={(e) => {
                    reset();
                    setNewValue(e.target.value);
                  }}
                />
              </label>
            )}
            <p>
              {action === "rename_key"
                ? "If the destination key already exists, its value is kept. The old key is removed, including on those monitors."
                : action === "apply"
                  ? "Existing values for this key will be replaced. Other tags and monitor settings stay unchanged."
                  : "Only the exact key/value pair changes. Other keys and values stay unchanged."}
            </p>
            {!global && selectedIds.length > 10000 && (
              <p role="alert">
                Select at most 10000 monitors for one atomic change. No monitors
                will be silently skipped.
              </p>
            )}
            {!global && selectedIds.length === 0 && (
              <p>
                Select monitors in the inventory first, or choose a global
                rename.
              </p>
            )}
            <button
              type="submit"
              className="add-button"
              disabled={
                !global &&
                (selectedIds.length === 0 || selectedIds.length > 10000)
              }
            >
              Preview change
            </button>
          </fieldset>
          {pending && (
            <p ref={workingRef} tabIndex={-1} role="status">
              Working…
            </p>
          )}
          {problem && (
            <p ref={errorRef} tabIndex={-1} role="alert" className="add-error">
              {problem}
            </p>
          )}
          {currentPreview && (
            <div className="bulk-tags-preview" role="status">
              <p ref={previewRef} tabIndex={-1}>
                {currentPreview.changed} monitors will change;{" "}
                {currentPreview.unchanged} unchanged.
              </p>
              <p>
                {action === "rename_key"
                  ? `${currentPreview.collisions} monitors already have the destination key; their existing value will be kept.`
                  : action === "apply"
                    ? `${currentPreview.collisions} existing values will be replaced.`
                    : "Other keys and values stay unchanged."}
              </p>
              <button
                ref={confirmRef}
                type="button"
                className="add-button add-button-primary"
                disabled={pending || currentPreview.changed === 0}
                onClick={() => void run(true)}
              >
                Confirm tag change
              </button>
            </div>
          )}
          {saved && (
            <p ref={resultRef} tabIndex={-1} role="status">
              Changed {saved.changed} monitors; {saved.unchanged} unchanged.
            </p>
          )}
          <button
            type="button"
            className="add-button"
            // Keep a final tab stop while the fieldset is disabled. This lets
            // the existing Drawer trap contain Tab from the progress message.
            // aria-disabled keeps the unavailable action announced; the guard
            // below prevents activation without removing it from tab order.
            aria-disabled={pending}
            onClick={() => { if (!busy.current) onClose(); }}
          >
            Close
          </button>
        </form>
      </Panel>
    </Drawer>
  );
}
