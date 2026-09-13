/**
 * The one place a request leaves this app, and the one place a 401 is noticed.
 *
 * Every screen used to call `fetch` directly and turn a non-2xx into a
 * sentence on its own. That was survivable while the only failure worth
 * naming was "the server said no", but an expired session is different in
 * kind: it is not this screen's problem, it is every screen's problem at
 * once. A dashboard that renders "could not load monitors: HTTP 401" is
 * telling the truth and still leaving the user stuck, because the thing they
 * need — a login form — is not on the page and no amount of retrying will
 * put it there.
 *
 * So the status is handled in one place: any 401 from any call announces
 * itself, and whoever owns the session decides what the screen becomes. The
 * caller keeps getting its error thrown, because a failed request is still a
 * failed request and the component that made it still has to stop rendering
 * a result it never received.
 */

/** Notified whenever the API rejects the credentials sent with a request. */
type UnauthorizedListener = () => void;

const unauthorizedListeners = new Set<UnauthorizedListener>();

/**
 * Subscribes to "your session is gone".
 *
 * A module-level set rather than React context because the fetchers are plain
 * functions: they are called from query functions, from event handlers, and
 * from tests that never mount a provider. Threading a context through all of
 * them would make the signal a parameter of every request in the app to serve
 * one subscriber.
 */
export function onUnauthorized(listener: UnauthorizedListener): () => void {
  unauthorizedListeners.add(listener);
  return () => {
    unauthorizedListeners.delete(listener);
  };
}

/** Announces a rejected credential. Exported for the fetchers and for tests. */
export function reportUnauthorized(): void {
  for (const listener of [...unauthorizedListeners]) listener();
}

/** Thrown for a non-2xx response, carrying the server's own sentence. */
export class ApiError extends Error {
  readonly status: number;
  /** Seconds to wait, from Retry-After, when the server rate-limited us. */
  readonly retryAfter: number | null;
  /**
   * The request field the server blamed, when it blamed one.
   *
   * `null` means the rejection was not about a single field — malformed JSON,
   * a rate limit, a server failure — and the message belongs in the form's
   * global region rather than under an input.
   *
   * This comes off the wire rather than being worked out here on purpose. The
   * alternative is classifying the error by matching on the server's wording,
   * which keeps working right up until a message is reworded and then fails
   * silently: the text still renders, just in the wrong place.
   */
  readonly field: string | null;

  constructor(
    status: number,
    message: string,
    retryAfter: number | null = null,
    field: string | null = null,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.retryAfter = retryAfter;
    this.field = field;
  }
}

/** Reads the API's error shape out of a response. */
export async function readError(res: Response): Promise<ApiError> {
  let message = `HTTP ${res.status}`;
  let field: string | null = null;
  try {
    const body = (await res.json()) as { error?: string; message?: string; field?: string };
    // The API's own wording is better than anything invented here: it
    // knows which field was wrong and what the right shape looks like.
    message = body.error ?? body.message ?? message;
    // An empty string is treated as absent. The server omits the key rather
    // than sending "", but a proxy or an older build might not, and a field
    // named "" would match no input and silently swallow the message.
    field = typeof body.field === "string" && body.field !== "" ? body.field : null;
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
    field,
  );
}

/**
 * A request to this instance's API.
 *
 * `credentials: "same-origin"` because the browser authenticates with the
 * session cookie; the API also accepts a bearer token, but that path belongs
 * to machines.
 *
 * The 401 is announced *before* the error is thrown so the session state has
 * already moved by the time the caller's catch block runs. Otherwise a screen
 * can render its own "unauthorized" message for one frame before the login
 * form replaces it, which reads as a flash of a broken page.
 */
export async function apiFetch(input: string, init: RequestInit = {}): Promise<Response> {
  const res = await fetch(input, {
    credentials: "same-origin",
    ...init,
    headers: { Accept: "application/json", ...init.headers },
  });
  if (res.status === 401) reportUnauthorized();
  return res;
}

/** A request whose non-2xx is an `ApiError`. */
export async function apiRequest(input: string, init: RequestInit = {}): Promise<Response> {
  const res = await apiFetch(input, init);
  if (!res.ok) throw await readError(res);
  return res;
}

/** A JSON request whose non-2xx is an `ApiError`. */
export async function apiJSON<T>(input: string, init: RequestInit = {}): Promise<T> {
  const res = await apiRequest(input, init);
  return (await res.json()) as T;
}

/** A JSON POST. The body is serialised here so no caller forgets the header. */
export async function apiPost(input: string, body: unknown, init: RequestInit = {}): Promise<Response> {
  return apiRequest(input, {
    method: "POST",
    ...init,
    headers: { "Content-Type": "application/json", ...init.headers },
    body: JSON.stringify(body),
  });
}
