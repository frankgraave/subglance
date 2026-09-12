/**
 * The "does this actually work?" call, and the model it feeds.
 *
 * Kept apart from the form so the form stays a pure function of props: the
 * form renders a `PreviewState`, it does not know what a fetch is. A test can
 * then drive every state — idle, checking, passed, failed, rate-limited —
 * without a network or a fake timer.
 */

export type PreviewRequest = {
  /** Omitted means "let the server infer it from the target". */
  type?: string;
  target: string;
  timeout_s?: number;
  method?: string;
  expected_status?: string;
  keyword?: string;
  keyword_mode?: string;
  follow_redirects?: boolean;
};

/** POST /api/v1/monitors/preview, as the server returns it. */
export type PreviewResult = {
  ok: boolean;
  checked_at: string;
  latency_ms: number;
  status_code?: number;
  kind?: string;
  error?: string;
  cert_expiry?: string;
  /** What was really probed, after the server's inference. May differ. */
  type: string;
  target: string;
};

/**
 * What the form shows about the last preview.
 *
 * `rejected` is separate from `failed` on purpose, and that distinction is the
 * whole reason this is a union rather than a result plus an error string.
 * "Your target is unreachable" and "I could not tell what you meant" call for
 * completely different next actions: the first says keep the address and go
 * look at the server, the second says fix the address. Collapsing them into
 * one red box would send people to debug a service that was never contacted.
 */
/**
 * A fingerprint of the inputs a preview was run with.
 *
 * `done` carries it because a result is only evidence about the request that
 * produced it. Comparing just the target was not enough: previewing
 * `example.com` (inferred as https) and then switching the type dropdown left
 * the result "valid", so the save used the stale inferred type and created a
 * monitor the user never tested.
 */
export type PreviewFingerprint = string;

export type PreviewState =
  | { phase: "idle" }
  | { phase: "checking" }
  | { phase: "done"; result: PreviewResult; request: PreviewFingerprint }
  | { phase: "rejected"; message: string };

/**
 * Everything about a request that changes what a probe proves, in one string.
 *
 * Interval and name are deliberately absent: neither is sent to the preview
 * endpoint, so changing them cannot invalidate a result, and treating them as
 * inputs would throw away a valid preview for nothing.
 */
export function fingerprintPreview(req: PreviewRequest): PreviewFingerprint {
  return JSON.stringify([
    req.target.trim(),
    req.type ?? "",
    req.timeout_s ?? 0,
    req.method ?? "",
    req.expected_status ?? "",
    req.keyword ?? "",
    req.keyword_mode ?? "",
    req.follow_redirects ?? null,
  ]);
}

/** Thrown for a non-2xx response, carrying the server's own sentence. */
export class ApiError extends Error {
  readonly status: number;
  /** Seconds to wait, from Retry-After, when the server rate-limited us. */
  readonly retryAfter: number | null;

  constructor(status: number, message: string, retryAfter: number | null = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.retryAfter = retryAfter;
  }
}

async function readError(res: Response): Promise<ApiError> {
  let message = `HTTP ${res.status}`;
  try {
    const body = (await res.json()) as { error?: string; message?: string };
    // The API's own wording is better than anything we could invent here: it
    // knows which field was wrong and what the right shape looks like.
    message = body.error ?? body.message ?? message;
  } catch {
    // A non-JSON error body (a proxy's HTML 502, say) leaves the status line,
    // which is still more use than throwing a parse error over the top of it.
  }
  const header = res.headers.get("Retry-After");
  const retryAfter = header === null ? null : Number.parseInt(header, 10);
  return new ApiError(
    res.status,
    message,
    retryAfter !== null && Number.isFinite(retryAfter) ? retryAfter : null,
  );
}

/** Probes a target without creating anything. */
export async function previewCheck(
  req: PreviewRequest,
  signal?: AbortSignal,
): Promise<PreviewResult> {
  const res = await fetch("/api/v1/monitors/preview", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(req),
    signal,
  });
  if (!res.ok) throw await readError(res);
  return (await res.json()) as PreviewResult;
}

/** Creates a monitor. Returns the id the server assigned. */
export async function createMonitor(
  body: Record<string, unknown>,
  signal?: AbortSignal,
): Promise<{ id: string }> {
  const res = await fetch("/api/v1/monitors", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(body),
    signal,
  });
  if (!res.ok) throw await readError(res);
  const created = (await res.json()) as { id: string | number };
  return { id: String(created.id) };
}

/**
 * A name for a monitor the user did not name.
 *
 * Every required field costs installs (DESIGN.md §7.2), and a name is the
 * emptiest kind of required: the person typing it has just typed the same
 * information as a URL. The host is what they would have written anyway, and
 * it is the one part of the address that is both short and recognisable —
 * `example.com`, not `https://example.com/health?full=1`.
 *
 * It is a suggestion, not a lock: the name field is shown pre-filled and stays
 * editable. A default nobody can see is the version of this that goes wrong.
 */
export function suggestName(target: string): string {
  const trimmed = target.trim();
  if (trimmed === "") return "";
  const withScheme = trimmed.includes("://") ? trimmed : `https://${trimmed}`;
  try {
    const host = new URL(withScheme).hostname;
    if (host !== "") return host;
  } catch {
    // Not URL-shaped — a bare `db.example.com:5432` parses, but an input that
    // is still half-typed may not. Fall through to the raw text.
  }
  // Strip a path and a port so a half-typed host still yields something the
  // user recognises rather than the whole line.
  return trimmed.split("/")[0].split(":")[0];
}

/**
 * One sentence describing a finished preview.
 *
 * A sentence rather than a status code and a latency side by side: the reason
 * this screen exists is to answer "did that work?", and a number the reader
 * has to interpret is not an answer.
 */
export function describePreview(result: PreviewResult): string {
  if (result.ok) {
    const code = result.status_code !== undefined ? ` HTTP ${result.status_code},` : "";
    return `${result.target} answered:${code} ${result.latency_ms} ms.`;
  }
  // The server's error is the specific one — a refused connection, a bad
  // certificate, a keyword that was missing. Repeating the target matters
  // because it may not be what was typed.
  return result.error !== undefined && result.error !== ""
    ? `${result.target} did not answer: ${result.error}`
    : `${result.target} did not answer.`;
}
