import { useEffect, useRef, useState } from "react";
import { FieldError } from "../components/FieldError";
import { registerLeaveGuard } from "../shell/leaveGuard";
import { replacePushUrl } from "./inventoryApi";
import { PushUrlReveal } from "./PushUrlReveal";

/**
 * Replacing a push monitor's URL, inside the drawer the monitor's page opens:
 * a question, then the new URL.
 *
 * **The question says what breaks.** Replacing is one press that makes every
 * job using the current URL fail, so the question names that before anything
 * is sent. Escape, the close button or "Keep the current URL" leave the
 * monitor exactly as it was. It is a plain confirmation rather than the
 * retyped name a delete asks for: nothing recorded is lost, and the URL is
 * usually being replaced because it leaked, when a slower path helps nobody.
 *
 * **The reveal is on hand before the request is sent.** The new URL exists in
 * one response only, and the old one is dead the moment that response is
 * written. This file is loaded on first use with the reveal imported
 * statically, so the button that sends the request cannot be on screen until
 * the reveal has arrived too: a chunk that fails to load leaves the old URL
 * working rather than replacing it with one nobody can see.
 *
 * **Closing over the URL asks first.** While the request is in flight and
 * while the URL is on screen, a leave guard holds the drawer, so a stray
 * Escape or a press on the scrim does not throw away the only copy.
 */
export type ReplacePushUrlProps = {
  id: string;
  name: string;
  /** Closes the drawer. */
  onClose: () => void;
  /** Called once the server has issued the new URL, so the caller can refetch. */
  onReplaced?: () => void;
  /** Injected in tests; defaults to the real endpoint. */
  replace?: typeof replacePushUrl;
};

type Step =
  | { step: "confirm"; error?: string }
  | { step: "replacing" }
  | { step: "replaced"; url: string };

export function ReplacePushUrl({ id, name, onClose, onReplaced, replace = replacePushUrl }: ReplacePushUrlProps) {
  const [state, setState] = useState<Step>({ step: "confirm" });
  const element = useRef<HTMLDivElement>(null);
  const holding = useRef(false);
  useEffect(() => registerLeaveGuard({
    element: () => element.current,
    dirty: () => holding.current,
    discard: () => { holding.current = false; },
    what: "push URL",
  }), []);

  async function confirm() {
    holding.current = true;
    setState({ step: "replacing" });
    try {
      const url = await replace(id);
      onReplaced?.();
      setState({ step: "replaced", url });
    } catch (error) {
      holding.current = false;
      const message = error instanceof Error ? error.message : String(error);
      setState({ step: "confirm", error: `Could not replace the push URL: ${message}` });
    }
  }

  const done = () => {
    holding.current = false;
    onClose();
  };

  if (state.step === "replaced") {
    return (
      <div ref={element}>
        <PushUrlReveal url={state.url} name={name} title={`${name} has a new push URL`} onDone={done} />
      </div>
    );
  }

  const busy = state.step === "replacing";
  return (
    <div ref={element} className="form-column">
      <p className="warn-note">
        The current URL of {name} stops working immediately. Every job that uses
        it has to be updated with the new one.
      </p>
      <p className="field-help">
        Heartbeats, incidents and uptime are kept, and the deadline stays where
        it is: a job that does not report on the new URL in time still turns the
        monitor down.
      </p>
      {state.step === "confirm" && state.error !== undefined && <FieldError>{state.error}</FieldError>}
      <div className="button-row">
        <button type="button" className="button button--primary" disabled={busy} onClick={() => void confirm()}>
          {busy ? "Replacing…" : "Replace the URL"}
        </button>
        <button type="button" className="button" disabled={busy} onClick={done}>
          Keep the current URL
        </button>
      </div>
    </div>
  );
}
