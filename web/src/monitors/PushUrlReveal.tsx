import { useEffect, useRef, useState } from "react";
import { curlLine, curlStatusLine } from "./push";

/**
 * The push URL, shown once and never again.
 *
 * The server hashes the token before storing it, so this string exists in
 * exactly one HTTP response and nowhere else. That single fact drives every
 * decision here.
 *
 * **It is a step, not a toast.** A notification that fades takes the only copy
 * of the URL with it. This replaces the form and has to be dismissed by hand.
 *
 * **A curl line, not just the URL.** What actually gets pasted into a crontab
 * is a command. Handing over the bare URL leaves the user to remember `-fsS`,
 * without which a failing curl prints HTML into cron’s mail and still exits 0
 * on a 500.
 *
 * **The status form is offered too.** `?status=$?` after the job turns the
 * monitor from "it ran" into "it worked", and it is a line nobody invents by
 * themselves.
 *
 * There is deliberately no copy-to-clipboard-only path: the text stays
 * selectable in a readonly input, because `navigator.clipboard` is unavailable
 * over plain HTTP on a non-localhost origin — which is how a self-hosted
 * install on a LAN address is usually first reached.
 */

export type PushUrlRevealProps = {
  /** The URL the job should ping. */
  url: string;
  /** The monitor’s name, so the screen says which one this belongs to. */
  name: string;
  /** Dismisses the reveal. The caller closes the dialog. */
  onDone?: () => void;
  /** Injected in tests; defaults to the real clipboard when there is one. */
  writeClipboard?: (text: string) => Promise<void>;
};

export function PushUrlReveal({
  url,
  name,
  onDone,
  writeClipboard,
}: PushUrlRevealProps) {
  const [copied, setCopied] = useState<string | null>(null);

  /*
   * The control that was pressed to get here — the form's submit button —
   * unmounts as this panel replaces it, so focus falls back to <body> and the
   * next Tab starts at the top of the document. Moving focus to the panel
   * keeps the keyboard path continuous and makes a screen reader announce the
   * new step, including the warning that this URL is not recoverable.
   */
  const panel = useRef<HTMLElement>(null);
  useEffect(() => {
    panel.current?.focus();
  }, []);

  const copy = (what: string, text: string) => {
    const write =
      writeClipboard ??
      (navigator.clipboard !== undefined
        ? (value: string) => navigator.clipboard.writeText(value)
        : null);
    if (write === null) {
      // No clipboard API. Saying so beats a button that silently does
      // nothing; the text is selectable either way.
      setCopied("none");
      return;
    }
    void write(text).then(
      () => setCopied(what),
      () => setCopied("failed"),
    );
  };

  return (
    <section
      ref={panel}
      tabIndex={-1}
      className="push-reveal"
      aria-labelledby="push-reveal-title"
    >
      <h2 id="push-reveal-title" className="add-title">
        {name} is waiting for its first report
      </h2>

      {/*
       * An alert, not a status: this is the one thing on the screen that
       * cannot be recovered, and it has to interrupt rather than wait to be
       * read in turn.
       */}
      <p role="alert" className="push-reveal-warn">
        This URL is shown once. It is stored hashed, so closing this panel loses
        it — a monitor whose URL was never saved has to be deleted and made
        again.
      </p>

      <div className="add-field">
        <label className="add-label" htmlFor="push-reveal-url">
          Push URL
        </label>
        <div className="push-reveal-row">
          <input
            id="push-reveal-url"
            className="add-input push-reveal-input"
            value={url}
            readOnly
            spellCheck={false}
            onFocus={(event) => event.currentTarget.select()}
          />
          <button
            type="button"
            className="add-button"
            onClick={() => copy("url", url)}
          >
            Copy
          </button>
        </div>
      </div>

      <div className="add-field">
        <label className="add-label" htmlFor="push-reveal-curl">
          For a cron line
        </label>
        <div className="push-reveal-row">
          <input
            id="push-reveal-curl"
            className="add-input push-reveal-input"
            value={curlLine(url)}
            readOnly
            spellCheck={false}
            onFocus={(event) => event.currentTarget.select()}
            aria-describedby="push-reveal-curl-help"
          />
          <button
            type="button"
            className="add-button"
            onClick={() => copy("curl", curlLine(url))}
          >
            Copy
          </button>
        </div>
        <p id="push-reveal-curl-help" className="add-help">
          Put it after the job, on the same line.
        </p>
      </div>

      <div className="add-field">
        <label className="add-label" htmlFor="push-reveal-status">
          To report failures too
        </label>
        <div className="push-reveal-row">
          <input
            id="push-reveal-status"
            className="add-input push-reveal-input"
            value={curlStatusLine(url)}
            readOnly
            spellCheck={false}
            onFocus={(event) => event.currentTarget.select()}
            aria-describedby="push-reveal-status-help"
          />
          <button
            type="button"
            className="add-button"
            onClick={() => copy("status", curlStatusLine(url))}
          >
            Copy
          </button>
        </div>
        <p id="push-reveal-status-help" className="add-help">
          Passes the job’s exit code along, so a backup that ran and failed
          shows as down instead of as a successful report.
        </p>
      </div>

      <div role="status" aria-live="polite" className="add-result-region">
        {copied === null ? null : copied === "failed" ? (
          <p className="add-result add-result-bad">
            Could not copy. Select the text and copy it by hand.
          </p>
        ) : copied === "none" ? (
          <p className="add-result add-result-bad">
            This browser will not give a page on an insecure origin access to
            the clipboard. Select the text and copy it by hand.
          </p>
        ) : (
          <p className="add-result add-result-good">Copied.</p>
        )}
      </div>

      <div className="add-actions">
        <button
          type="button"
          className="add-button add-button-primary"
          onClick={onDone}
        >
          I have saved it
        </button>
      </div>
    </section>
  );
}
