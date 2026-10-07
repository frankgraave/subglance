// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { AccountSessions, SessionsPanel } from "./Sessions";
import { sessionName } from "./sessionName";
import { sampleSessions } from "./fixtures";
import type { BrowserSession } from "./api";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

type Handler = (url: string, init?: RequestInit) => Response | undefined;

/** A server holding `sessions`, which DELETE and end-others really change. */
function serve(sessions: BrowserSession[] = sampleSessions, onRequest?: Handler) {
  let list = sessions;
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
    const answer = onRequest?.(url, init);
    if (answer) return answer;
    if (url === "/api/v1/sessions/end-others" && init?.method === "POST") {
      const ended = list.filter((s) => !s.current).length;
      list = list.filter((s) => s.current);
      return json({ ended });
    }
    if (url.startsWith("/api/v1/sessions/") && init?.method === "DELETE") {
      const id = url.split("/").pop();
      if (!list.some((s) => s.id === id)) return json({ error: "session not found" }, 404);
      list = list.filter((s) => s.id !== id);
      return new Response(null, { status: 204 });
    }
    if (/^\/api\/v1\/users\/\d+\/sessions$/.test(url) && init?.method === "DELETE") {
      const ended = list.length;
      list = [];
      return json({ ended });
    }
    return json({ sessions: list });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  return { fetcher, client };
}

function mountOwn(sessions?: BrowserSession[], onRequest?: Handler) {
  const { fetcher, client } = serve(sessions, onRequest);
  render(<QueryClientProvider client={client}><SessionsPanel /></QueryClientProvider>);
  return fetcher;
}

function mountAccount(sessions?: BrowserSession[], onRequest?: Handler) {
  const { fetcher, client } = serve(sessions, onRequest);
  render(<QueryClientProvider client={client}><AccountSessions userId={2} email="oncall@example.com" /></QueryClientProvider>);
  return fetcher;
}

const calls = (fetcher: ReturnType<typeof vi.fn>, method: string) =>
  fetcher.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === method).map(([url]) => url);

it("names a session by browser and platform, and falls back when the server could not", () => {
  expect(sessionName(sampleSessions[0])).toBe("Firefox on macOS");
  expect(sessionName({ ...sampleSessions[0], platform: "" })).toBe("Firefox");
  expect(sessionName(sampleSessions[2])).toBe("Unrecognised browser");
});

it("lists your sessions with browser, address and last use, and marks this browser", async () => {
  mountOwn();
  const list = await screen.findByRole("list", { name: "Your sessions" });
  const rows = within(list).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(within(rows[0]).getByText("Firefox on macOS")).toBeTruthy();
  expect(within(rows[0]).getByText("this browser")).toBeTruthy();
  expect(within(rows[0]).getByText(/2001:db8::10/)).toBeTruthy();
  expect(within(rows[0]).getByText(/in use now/)).toBeTruthy();
  // The current session has no Sign out of its own: the sidebar's ends it.
  expect(within(rows[0]).queryByRole("button")).toBeNull();
  expect(within(rows[1]).getByText("Safari on iPhone")).toBeTruthy();
  expect(within(rows[1]).getByText(/last seen 6 Oct 2026/)).toBeTruthy();
  // A client the server could not name shows its header instead.
  expect(within(rows[2]).getByText("homegrown-client/2.1")).toBeTruthy();
});

it("signs one other session out and refreshes the list", async () => {
  const fetcher = mountOwn();
  const button = await screen.findByRole("button", { name: /^Sign out Safari on iPhone at 2001:db8::20/ });
  fireEvent.click(button);
  expect(await screen.findByText("Signed out Safari on iPhone.")).toBeTruthy();
  expect(calls(fetcher, "DELETE")).toEqual([`/api/v1/sessions/${"b".repeat(32)}`]);
  await waitFor(() => expect(screen.queryByText("Safari on iPhone")).toBeNull());
  expect(within(screen.getByRole("list", { name: "Your sessions" })).getAllByRole("listitem")).toHaveLength(2);
});

it("shows the server's refusal when a session cannot be ended", async () => {
  mountOwn(sampleSessions, (_url, init) => init?.method === "DELETE" ? json({ error: "could not end the session" }, 500) : undefined);
  fireEvent.click(await screen.findByRole("button", { name: /^Sign out Safari on iPhone/ }));
  expect(await screen.findByText("could not end the session")).toBeTruthy();
  expect(screen.getByText("Safari on iPhone")).toBeTruthy();
});

it("signs out everywhere else and says how many", async () => {
  const fetcher = mountOwn();
  // Resting until the list says there is someone else to sign out.
  expect((screen.getByRole("button", { name: "Sign out everywhere else" }) as HTMLButtonElement).disabled).toBe(true);
  await screen.findByRole("list", { name: "Your sessions" });
  fireEvent.click(screen.getByRole("button", { name: "Sign out everywhere else" }));
  expect(await screen.findByText("Signed out 2 other sessions.")).toBeTruthy();
  expect(calls(fetcher, "POST")).toEqual(["/api/v1/sessions/end-others"]);
  await waitFor(() => expect(within(screen.getByRole("list", { name: "Your sessions" })).getAllByRole("listitem")).toHaveLength(1));
  // Nothing left to sign out, so the button rests.
  expect((screen.getByRole("button", { name: "Sign out everywhere else" }) as HTMLButtonElement).disabled).toBe(true);
});

it("refuses an off-shape list rather than guessing which row is this browser", async () => {
  mountOwn(undefined, (_url, init) => !init?.method ? json({ sessions: [{ id: "x" }] }) : undefined);
  expect(await screen.findByText("Sessions unavailable.")).toBeTruthy();
  expect(screen.queryByRole("list", { name: "Your sessions" })).toBeNull();
});

// Without `current`, every row would look like someone else's and offer
// "Sign out" on the browser the reader is using.
it("refuses a list that does not say which session is this browser", async () => {
  const unmarked = sampleSessions.map(({ current: _current, ...rest }) => rest);
  mountOwn(undefined, (_url, init) => !init?.method ? json({ sessions: unmarked }) : undefined);
  expect(await screen.findByText("Sessions unavailable.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Sign out Firefox on macOS/ })).toBeNull();
});

it("shows an administrator another account's sessions, with addresses, and signs it out everywhere", async () => {
  const others = sampleSessions.map((s) => ({ ...s, current: false }));
  const fetcher = mountAccount(others);
  const list = await screen.findByRole("list", { name: "Sessions of oncall@example.com" });
  expect(within(list).getAllByRole("listitem")).toHaveLength(3);
  expect(within(list).getByText(/2001:db8::20/)).toBeTruthy();
  // No per-session button here: one action answers a lost device.
  expect(within(list).queryAllByRole("button")).toHaveLength(0);
  expect(fetcher.mock.calls[0][0]).toBe("/api/v1/users/2/sessions");

  fireEvent.click(screen.getByRole("button", { name: "Sign out everywhere for oncall@example.com" }));
  expect(await screen.findByText("oncall@example.com is signed out of 3 sessions.")).toBeTruthy();
  expect(calls(fetcher, "DELETE")).toEqual(["/api/v1/users/2/sessions"]);
  expect(await screen.findByText("Not signed in anywhere.")).toBeTruthy();
});
