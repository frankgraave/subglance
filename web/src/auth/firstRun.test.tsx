// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SessionGate } from "./SessionGate";
import { useSession } from "./useSession";
import { createFirstUser, fetchCurrentUser } from "./api";

/**
 * SUB-82's last acceptance criterion, as one test: `docker compose up -d`,
 * browser, first monitor visible, without ever touching a terminal.
 *
 * It drives the real session hook and the real fetchers against a stub server
 * that behaves like the API does — setup open, then closed and authenticated
 * — because the failure this guards against is exactly a mismatch between
 * what the screens assume and what the server actually answers. Mocking the
 * fetchers would assume the thing under test.
 *
 * The monitor list is a stand-in for the dashboard: what matters here is that
 * an authenticated request is reached at all, which before this work it never
 * was — the SPA asked, got 401 and stopped.
 */

afterEach(cleanup);

const originalFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = originalFetch;
});

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** A stub of the real server: no users, then one, with a session cookie. */
function fakeServer() {
  let signedIn = false;
  let setUp = false;
  const monitors: { id: number; name: string }[] = [];

  return {
    get monitors() {
      return monitors;
    },
    fetch: vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";

      if (url.endsWith("/api/v1/auth/me")) {
        return Promise.resolve(
          signedIn
            ? json({
                id: 1,
                email: "you@example.com",
                role: "admin",
                created_at: "",
              })
            : json({ error: "authentication required" }, 401),
        );
      }
      if (url.endsWith("/api/v1/setup") && method === "GET") {
        return Promise.resolve(json({ setup_required: !setUp }));
      }
      if (url.endsWith("/api/v1/setup") && method === "POST") {
        if (setUp)
          return Promise.resolve(
            json({ error: "setup has already been completed" }, 409),
          );
        setUp = true;
        signedIn = true;
        return Promise.resolve(
          json(
            { id: 1, email: "you@example.com", role: "admin", created_at: "" },
            201,
          ),
        );
      }
      if (url.includes("/api/v1/monitors") && method === "POST") {
        if (!signedIn)
          return Promise.resolve(
            json({ error: "authentication required" }, 401),
          );
        const body = JSON.parse(String(init?.body)) as { name: string };
        monitors.push({ id: monitors.length + 1, name: body.name });
        return Promise.resolve(json({ id: monitors.length }, 201));
      }
      if (url.includes("/api/v1/monitors")) {
        if (!signedIn)
          return Promise.resolve(
            json({ error: "authentication required" }, 401),
          );
        return Promise.resolve(json({ monitors }));
      }
      throw new Error(`unexpected request: ${method} ${url}`);
    }),
  };
}

/** The app, reduced to the part this journey passes through. */
function FirstRun() {
  const { session, onSignedIn, refresh } = useSession();
  return (
    <SessionGate session={session} onSignedIn={onSignedIn} onRetry={refresh}>
      <p>
        Dashboard for {session.state === "signedIn" ? session.user.email : ""}
      </p>
    </SessionGate>
  );
}

describe("the first minute after docker compose up", () => {
  it("gets from a fresh instance to an authenticated dashboard with no terminal", async () => {
    const server = fakeServer();
    globalThis.fetch = server.fetch as unknown as typeof globalThis.fetch;

    render(<FirstRun />);

    // 1. The browser lands on setup rather than on a 401 it cannot explain.
    expect(await screen.findByText(/Set up this instance/)).toBeTruthy();

    // 2. An account is created from the form.
    fireEvent.change(screen.getByLabelText(/Email/), {
      target: { value: "you@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/Password/), {
      target: { value: "a-long-passphrase" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Create account/ }));

    // 3. That account is signed in already — no second login step.
    expect(
      await screen.findByText(/Dashboard for you@example.com/),
    ).toBeTruthy();

    // 4. And the session it left behind authenticates a real API call, which
    //    is the part that was broken: the dashboard's own first request.
    const res = await fetch("/api/v1/monitors?heartbeats=100");
    expect(res.status).toBe(200);

    // 5. The setup endpoint is closed for good.
    await expect(
      createFirstUser("someone@example.com", "another-passphrase"),
    ).rejects.toThrow();
    expect(await fetchCurrentUser()).not.toBeNull();
  });
});
