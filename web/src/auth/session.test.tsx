// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch, reportUnauthorized } from "../api/http";
import { SessionGate } from "./SessionGate";
import { useSession } from "./useSession";

/**
 * SUB-82's acceptance criteria, as behaviour:
 *
 *   - a first run lands on the setup screen, not on a broken dashboard;
 *   - completing setup lands on the app, signed in;
 *   - a 401 from anywhere sends a signed-in user to the login screen;
 *   - signing out exists, is findable, and works;
 *   - an unreachable server is not presented as "please log in".
 */

afterEach(cleanup);
afterEach(() => window.localStorage.clear());

type Fetch = typeof globalThis.fetch;

/** A fetch stub that answers by URL and method. */
function stubFetch(routes: Record<string, () => Response>): Fetch {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : String(input);
    const key = `${init?.method ?? "GET"} ${url}`;
    const route = routes[key];
    if (route === undefined) throw new Error(`unexpected request: ${key}`);
    return Promise.resolve(route());
  }) as unknown as Fetch;
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Renders the gate over a fake app, driven by the real hook. */
function Harness() {
  const { session, onSignedIn, signOut, refresh } = useSession();
  return (
    <SessionGate session={session} onSignedIn={onSignedIn} onRetry={refresh}>
      <div>
        <p>the dashboard</p>
        <button type="button" onClick={signOut}>
          Sign out
        </button>
      </div>
    </SessionGate>
  );
}

const originalFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = originalFetch;
});

describe("resolving the session", () => {
  it("shows the setup screen on a first run", async () => {
    globalThis.fetch = stubFetch({
      "GET /api/v1/auth/me": () => json({ error: "authentication required" }, 401),
      "GET /api/v1/setup": () => json({ setup_required: true }),
    });

    render(<Harness />);

    expect(await screen.findByText(/Set up this instance/)).toBeTruthy();
    // The acceptance criterion is specifically that a first run does not land
    // on the app with a failed request behind it.
    expect(screen.queryByText("the dashboard")).toBeNull();
  });

  it("shows the login screen when an account already exists", async () => {
    globalThis.fetch = stubFetch({
      "GET /api/v1/auth/me": () => json({ error: "authentication required" }, 401),
      "GET /api/v1/setup": () => json({ setup_required: false }),
    });

    render(<Harness />);

    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
    expect(screen.queryByText(/Set up this instance/)).toBeNull();
  });

  it("goes straight to the app for a browser that already has a session", async () => {
    const fetcher = stubFetch({
      "GET /api/v1/auth/me": () => json({ id: 1, email: "me@example.com", role: "admin", created_at: "" }),
    });
    globalThis.fetch = fetcher;

    render(<Harness />);

    expect(await screen.findByText("the dashboard")).toBeTruthy();
    // One request, not two: the setup status is only worth asking for when
    // there is no session, which is the rarer case by a wide margin.
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("does not offer a login form when the server cannot be reached", async () => {
    globalThis.fetch = vi.fn(() => Promise.reject(new TypeError("Failed to fetch"))) as unknown as Fetch;

    render(<Harness />);

    expect(await screen.findByText(/Cannot reach this instance/)).toBeTruthy();
    // Typing a password into a page that cannot check it is worse than being
    // told the truth: the attempt would be reported back as a failed login.
    expect(screen.queryByLabelText(/Password/)).toBeNull();
  });

  it("retries from the error screen", async () => {
    let attempt = 0;
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      attempt += 1;
      if (attempt === 1) return Promise.reject(new TypeError("Failed to fetch"));
      if (url.includes("/auth/me")) {
        return Promise.resolve(json({ id: 1, email: "me@example.com", role: "admin", created_at: "" }));
      }
      return Promise.resolve(json({ setup_required: false }));
    }) as unknown as Fetch;

    render(<Harness />);
    fireEvent.click(await screen.findByRole("button", { name: /Try again/ }));

    expect(await screen.findByText("the dashboard")).toBeTruthy();
  });
});

describe("losing and ending a session", () => {
  it("sends a signed-in user to the login screen when any request gets a 401", async () => {
    globalThis.fetch = stubFetch({
      "GET /api/v1/auth/me": () => json({ id: 1, email: "me@example.com", role: "admin", created_at: "" }),
    });

    render(<Harness />);
    expect(await screen.findByText("the dashboard")).toBeTruthy();

    // Exactly what a background refetch on an expired cookie does, without
    // needing a second screen to make the request.
    act(() => reportUnauthorized());

    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
    expect(screen.queryByText("the dashboard")).toBeNull();
  });

  it("treats a 401 from a real request as the end of the session", async () => {
    /*
     * The test above drives the signal directly, which proves the session
     * reacts but not that anything ever raises it. Removing the status check
     * from `apiFetch` left that test green — so this one makes an actual
     * request and lets the response carry the 401, which is the path every
     * screen in the app really takes.
     */
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/auth/me")) {
        return Promise.resolve(json({ id: 1, email: "me@example.com", role: "admin", created_at: "" }));
      }
      return Promise.resolve(json({ error: "authentication required" }, 401));
    }) as unknown as Fetch;

    render(<Harness />);
    expect(await screen.findByText("the dashboard")).toBeTruthy();

    await act(async () => {
      await apiFetch("/api/v1/monitors");
    });

    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
  });

  it("signs out without waiting for the server", async () => {
    const logout = vi.fn(() => new Promise<Response>(() => {}));
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/auth/logout")) return logout();
      if (url.includes("/auth/me")) {
        return Promise.resolve(json({ id: 1, email: "me@example.com", role: "admin", created_at: "" }));
      }
      return Promise.resolve(json({ setup_required: false }));
    }) as unknown as Fetch;

    render(<Harness />);
    fireEvent.click(await screen.findByRole("button", { name: /Sign out/ }));

    // The request never resolves; the screen must change anyway, or a slow
    // server leaves a signed-out user looking at a dashboard.
    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
    expect(logout).toHaveBeenCalled();
  });

  it("does not restore the session on reload when the sign-out request failed", async () => {
    /*
     * The hole this closes: the cookie is HttpOnly, so a logout that never
     * reached the server leaves it valid, and the next load discovers it
     * through `/auth/me` and signs the browser back in — after the user has
     * been shown the login screen.
     */
    const me = () => json({ id: 1, email: "me@example.com", role: "admin", created_at: "" });
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/auth/logout")) return Promise.reject(new TypeError("Failed to fetch"));
      if (url.includes("/auth/me")) return Promise.resolve(me());
      return Promise.resolve(json({ setup_required: false }));
    }) as unknown as Fetch;

    const first = render(<Harness />);
    fireEvent.click(await screen.findByRole("button", { name: /Sign out/ }));
    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
    first.unmount();

    // A fresh mount is what a reload looks like from here, and `/auth/me`
    // would still answer with a user.
    render(<Harness />);
    expect(await screen.findByText(/Cannot reach this instance/)).toBeTruthy();
    expect(screen.queryByText("the dashboard")).toBeNull();
  });

  it("retries the failed sign-out on the next load and then stops", async () => {
    let logouts = 0;
    globalThis.fetch = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/auth/logout")) {
        logouts += 1;
        if (logouts === 1) return Promise.reject(new TypeError("Failed to fetch"));
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      // The cookie stays valid until a logout actually reaches the server,
      // which is the whole reason the retry has to run before this question
      // is asked.
      if (url.includes("/auth/me")) {
        if (logouts >= 2) return Promise.resolve(json({ error: "authentication required" }, 401));
        return Promise.resolve(json({ id: 1, email: "me@example.com", role: "admin", created_at: "" }));
      }
      return Promise.resolve(json({ setup_required: false }));
    }) as unknown as Fetch;

    const first = render(<Harness />);
    fireEvent.click(await screen.findByRole("button", { name: /Sign out/ }));
    await waitFor(() => expect(logouts).toBe(1));
    first.unmount();

    // The retry succeeds, so this load reaches the login screen...
    const second = render(<Harness />);
    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
    await waitFor(() => expect(logouts).toBe(2));
    second.unmount();

    // ...and the intent is cleared: a settled sign-out must not be replayed
    // on every load for the rest of the browser's life.
    render(<Harness />);
    expect(await screen.findByRole("button", { name: /Sign in/ })).toBeTruthy();
    expect(logouts).toBe(2);
  });

  it("keeps a signed-out browser on the form when a public request gets a 401", async () => {
    globalThis.fetch = stubFetch({
      "GET /api/v1/auth/me": () => json({ error: "authentication required" }, 401),
      "GET /api/v1/setup": () => json({ setup_required: true }),
    });

    render(<Harness />);
    expect(await screen.findByText(/Set up this instance/)).toBeTruthy();

    act(() => reportUnauthorized());

    // Still the setup screen. Re-announcing "anonymous" would reset a form
    // the user is halfway through typing into.
    await waitFor(() => {
      expect(screen.getByText(/Set up this instance/)).toBeTruthy();
    });
  });
});
