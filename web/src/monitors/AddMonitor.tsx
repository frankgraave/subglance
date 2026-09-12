import { useCallback, useRef, useState } from "react";
import { AddMonitorForm } from "./AddMonitorForm";
import type { AddMonitorValues } from "./AddMonitorForm";
import { ApiError, createMonitor, fingerprintPreview, previewCheck } from "./preview";
import type { PreviewRequest, PreviewState } from "./preview";

/**
 * The data owner for the add-monitor form.
 *
 * It exists so `AddMonitorForm` can stay a pure function of props: everything
 * that touches the network, aborts an in-flight request or turns an HTTP
 * status into a sentence lives here, and a test can render the form directly
 * with a hand-made `PreviewState`.
 */

export type AddMonitorProps = {
  /** Called with the new monitor's id once the server accepted it. */
  onCreated?: (id: string) => void;
  onCancel?: () => void;
  /** Injected in tests. Defaults to the real endpoints. */
  api?: {
    preview: typeof previewCheck;
    create: typeof createMonitor;
  };
};

export function AddMonitor({ onCreated, onCancel, api }: AddMonitorProps) {
  const preview = api?.preview ?? previewCheck;
  const create = api?.create ?? createMonitor;

  const [state, setState] = useState<PreviewState>({ phase: "idle" });
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  /*
   * The in-flight preview, so a second press supersedes the first.
   *
   * Without this, pressing "Test it" twice can land the slow first answer
   * *after* the fast second one and leave the form claiming a result for a
   * target the user has already corrected — the one failure mode that makes a
   * pre-save check worse than no check.
   */
  const inFlight = useRef<AbortController | null>(null);

  const runPreview = useCallback(
    (values: AddMonitorValues) => {
      inFlight.current?.abort();
      const controller = new AbortController();
      inFlight.current = controller;
      setState({ phase: "checking" });

      const request = previewRequestFor(values);

      void (async () => {
        try {
          const result = await preview(request, controller.signal);
          // The fingerprint travels with the result, so the save can ask "was
          // this run with what is in the form right now?" instead of guessing
          // from the target alone.
          if (!controller.signal.aborted) {
            setState({ phase: "done", result, request: fingerprintPreview(request) });
          }
        } catch (error) {
          if (controller.signal.aborted) return;
          setState({ phase: "rejected", message: explain(error) });
        }
      })();
    },
    [preview],
  );

  const save = useCallback(
    (values: AddMonitorValues) => {
      setSaving(true);
      setSaveError(null);
      // Inference lives on the server and runs only for a preview, so without
      // a matching one there is nothing to infer from: the old `http` fallback
      // sent a bare hostname to an endpoint that requires a scheme, and the
      // user got a validation error about a field they never filled in.
      if (values.type === "" && !previewMatches(values, state)) {
        setSaving(false);
        setSaveError(
          "press Test it first, or pick a type — SubGlance works out what a bare address means" +
            " by probing it, and the settings changed since the last test",
        );
        return;
      }

      void (async () => {
        try {
          const { id } = await create({
            name: values.name.trim() !== "" ? values.name.trim() : values.target.trim(),
            // The server infers the type for a preview, but creating a monitor
            // requires one. Reusing what the preview resolved is why the
            // preview echoes it: the alternative is a second copy of the
            // inference rules in TypeScript, drifting from the Go one.
            type: resolvedType(values, state),
            target: resolvedTarget(values, state),
            interval_s: values.intervalS,
            timeout_s: values.timeoutS,
            ...(values.keyword !== ""
              ? { keyword: values.keyword, keyword_mode: values.keywordMode }
              : {}),
          });
          onCreated?.(id);
        } catch (error) {
          setSaveError(explain(error));
        } finally {
          setSaving(false);
        }
      })();
    },
    [create, onCreated, state],
  );

  return (
    <AddMonitorForm
      onPreview={runPreview}
      onSubmit={save}
      preview={state}
      saving={saving}
      saveError={saveError}
      onCancel={onCancel}
    />
  );
}

/** The preview request a given set of form values would send. */
function previewRequestFor(values: AddMonitorValues): PreviewRequest {
  return {
    target: values.target.trim(),
    ...(values.type !== "" ? { type: values.type } : {}),
    timeout_s: values.timeoutS,
    ...(values.keyword !== ""
      ? { keyword: values.keyword, keyword_mode: values.keywordMode }
      : {}),
  };
}

/**
 * Whether the last completed preview describes the form as it stands now.
 *
 * Fingerprint equality rather than a target comparison. Comparing targets
 * alone meant a preview stayed "valid" across a change to the type, the
 * timeout or the keyword: previewing `example.com` (inferred https) and then
 * choosing Ping saved a ping monitor for a target that had only ever been
 * tested over HTTPS.
 */
function previewMatches(values: AddMonitorValues, state: PreviewState): boolean {
  return state.phase === "done" && state.request === fingerprintPreview(previewRequestFor(values));
}

/**
 * The type to save with.
 *
 * A preview of these exact settings is authoritative, because the server ran
 * the inference and told us what it decided. Otherwise the explicit choice
 * wins — and `save` refuses before it gets here when there is neither.
 */
function resolvedType(values: AddMonitorValues, state: PreviewState): string {
  if (values.type !== "") return values.type;
  if (state.phase === "done" && previewMatches(values, state)) return state.result.type;
  return "http";
}

/**
 * The normalised target to save.
 *
 * The server normalises (`example.com` becomes `https://example.com`), and
 * saving its answer rather than the raw text is what keeps the created monitor
 * identical to the one that was tested.
 */
function resolvedTarget(values: AddMonitorValues, state: PreviewState): string {
  const typed = values.target.trim();
  if (state.phase === "done" && previewMatches(values, state)) return state.result.target;
  return typed;
}

/** Turns a thrown value into the sentence the form shows. */
function explain(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 429 && error.retryAfter !== null) {
      return `${error.message} (about ${error.retryAfter}s)`;
    }
    return error.message;
  }
  // A network-level failure never reached the server, so there is no server
  // sentence to quote. Say which half broke rather than printing "Failed to
  // fetch", which reads as though the target were down.
  return error instanceof Error
    ? `could not reach SubGlance itself: ${error.message}`
    : "could not reach SubGlance itself";
}
