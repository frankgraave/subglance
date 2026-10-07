// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { UsersCard } from "./Users";
import { twoUsers } from "./fixtures";
import type { Account } from "./api";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

type Handler = (url: string, init?: RequestInit) => Response | undefined;

function mount(users: Account[] = twoUsers, onRequest?: Handler) {
  let list = users;
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
    const answer = onRequest?.(url, init);
    if (answer) return answer;
    if (init?.method === "PATCH") {
      const id = Number(url.split("/").pop());
      const role = JSON.parse(String(init.body)).role;
      list = list.map((u) => u.id === id ? { ...u, role } : u);
      return json(list.find((u) => u.id === id));
    }
    if (init?.method === "DELETE") {
      const id = Number(url.split("/").pop());
      list = list.filter((u) => u.id !== id);
      return new Response(null, { status: 204 });
    }
    if (init?.method === "POST") {
      const body = JSON.parse(String(init.body));
      const created = { id: 3, email: body.email, role: body.role, created_at: "2026-09-26T00:00:00Z" };
      list = [...list, created];
      return json(created, 201);
    }
    return json({ users: list });
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><UsersCard userId={1} /></QueryClientProvider>);
  return fetcher;
}

const calls = (fetcher: ReturnType<typeof vi.fn>, method: string) =>
  fetcher.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === method);

/** The segment drawn as chosen in the role group with this name. */
function pressedRole(name: string) {
  const pressed = within(screen.getByRole("group", { name })).getAllByRole("button")
    .filter((button) => button.getAttribute("aria-pressed") === "true");
  expect(pressed).toHaveLength(1);
  return pressed[0].textContent;
}

/** Press one role in the role group with this name. */
function pickRole(name: string, role: string) {
  fireEvent.click(within(screen.getByRole("group", { name })).getByRole("button", { name: role }));
}

it("lists every account with its role, and marks your own", async () => {
  mount();
  expect(await screen.findByText("oncall@example.com")).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Users (2)" })).toBeTruthy();
  expect(pressedRole("Role for oncall@example.com")).toBe("Viewer");
  expect(screen.getByText("you")).toBeTruthy();
});

// The one administrator on the page is the one person who could not undo
// demoting or removing themselves, so their own row offers neither.
it("offers no role change and no removal on your own row", async () => {
  mount();
  await screen.findByText("oncall@example.com");
  expect(screen.queryByRole("group", { name: "Role for operator@example.com" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove operator@example.com" })).toBeNull();
  expect(screen.getByRole("button", { name: "Remove oncall@example.com" })).toBeTruthy();
});

it("changes a role only when the change is saved", async () => {
  const fetcher = mount();
  await screen.findByRole("group", { name: "Role for oncall@example.com" });
  pickRole("Role for oncall@example.com", "Editor");
  expect(pressedRole("Role for oncall@example.com")).toBe("Editor");
  expect(calls(fetcher, "PATCH")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Save role for oncall@example.com" }));
  expect(await screen.findByText("oncall@example.com is now an editor.")).toBeTruthy();
  const [url, init] = calls(fetcher, "PATCH")[0];
  expect(url).toBe("/api/v1/users/2");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ role: "editor" });
  await waitFor(() => expect(pressedRole("Role for oncall@example.com")).toBe("Editor"));
  expect(screen.queryByRole("button", { name: "Save role for oncall@example.com" })).toBeNull();
});

it("holds the role still while its change is being saved", async () => {
  let release: (response: Response) => void = () => {};
  const fetcher = mount();
  const answer = fetcher.getMockImplementation()!;
  // The PATCH answers only when the test says so, so "saving" can be looked at.
  fetcher.mockImplementation((url: string, init?: RequestInit) => init?.method === "PATCH"
    ? new Promise<Response>((resolve) => { release = resolve; }) : answer(url, init));
  await screen.findByRole("group", { name: "Role for oncall@example.com" });
  pickRole("Role for oncall@example.com", "Editor");
  fireEvent.click(screen.getByRole("button", { name: "Save role for oncall@example.com" }));
  const segments = within(screen.getByRole("group", { name: "Role for oncall@example.com" })).getAllByRole("button");
  await waitFor(() => expect(segments.every((button) => (button as HTMLButtonElement).disabled)).toBe(true));
  expect(pressedRole("Role for oncall@example.com")).toBe("Editor");
  release(json({ ...twoUsers[1], role: "editor" }));
  expect(await screen.findByText("oncall@example.com is now an editor.")).toBeTruthy();
});

it("puts a draft role back with Undo, without asking the server", async () => {
  const fetcher = mount();
  await screen.findByRole("group", { name: "Role for oncall@example.com" });
  pickRole("Role for oncall@example.com", "Admin");
  fireEvent.click(screen.getByRole("button", { name: "Undo" }));
  expect(pressedRole("Role for oncall@example.com")).toBe("Viewer");
  expect(calls(fetcher, "PATCH")).toHaveLength(0);
});

it("shows the server's refusal on the row it is about", async () => {
  mount(twoUsers, (_url, init) => init?.method === "PATCH"
    ? json({ error: "cannot demote the last administrator", field: "role" }, 400) : undefined);
  await screen.findByRole("group", { name: "Role for oncall@example.com" });
  pickRole("Role for oncall@example.com", "Editor");
  fireEvent.click(screen.getByRole("button", { name: "Save role for oncall@example.com" }));
  expect((await screen.findByRole("alert")).textContent).toBe("cannot demote the last administrator");
});

// DESIGN.md §7.5: removal asks for the address to be retyped, not for a
// second click, because it signs the account out and revokes its tokens.
it("removes an account only after its address is retyped exactly", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Remove oncall@example.com" }));
  expect(screen.getByText(/API tokens are revoked/)).toBeTruthy();
  const confirm = screen.getByRole("button", { name: "Delete oncall@example.com" });
  expect(confirm.hasAttribute("disabled")).toBe(true);
  fireEvent.click(confirm);
  fireEvent.change(screen.getByLabelText(/to confirm/i), { target: { value: "ONCALL@example.com" } });
  expect(confirm.hasAttribute("disabled")).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Keep it" }));
  expect(calls(fetcher, "DELETE")).toHaveLength(0);
  expect(screen.queryByLabelText(/to confirm/i)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Remove oncall@example.com" }));
  fireEvent.change(screen.getByLabelText(/to confirm/i), { target: { value: "oncall@example.com" } });
  fireEvent.click(screen.getByRole("button", { name: "Delete oncall@example.com" }));
  expect(await screen.findByText("oncall@example.com was removed.")).toBeTruthy();
  expect(calls(fetcher, "DELETE")[0][0]).toBe("/api/v1/users/2");
  await waitFor(() => expect(screen.queryByText("oncall@example.com")).toBeNull());
  expect(screen.getByRole("heading", { name: "Users (1)" })).toBeTruthy();
});

it("adds a viewer by default, and waits for a password of the minimum length", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add user" }));
  const form = screen.getByRole("form", { name: "Add user" });
  expect(pressedRole("Role")).toBe("Viewer");
  expect(within(screen.getByRole("group", { name: "Role" })).getAllByRole("button").map((b) => b.textContent))
    .toEqual(["Viewer", "Editor", "Admin"]);
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: " new@example.com " } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "short" } });
  const submit = form.querySelector("button[type=submit]") as HTMLButtonElement;
  expect(submit.disabled).toBe(true);
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct-horse-battery" } });
  expect(submit.disabled).toBe(false);
  fireEvent.click(submit);
  expect(await screen.findByText("new@example.com was added as a viewer.")).toBeTruthy();
  expect(JSON.parse(String((calls(fetcher, "POST")[0][1] as RequestInit).body)))
    .toEqual({ email: "new@example.com", password: "correct-horse-battery", role: "viewer" });
  expect(screen.queryByRole("form", { name: "Add user" })).toBeNull();
  expect(await screen.findByRole("group", { name: "Role for new@example.com" })).toBeTruthy();
});

it("keeps the form and its input when the server refuses a new account", async () => {
  mount(twoUsers, (_url, init) => init?.method === "POST"
    ? json({ error: "could not create the account: email already in use" }, 400) : undefined);
  fireEvent.click(await screen.findByRole("button", { name: "Add user" }));
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: "oncall@example.com" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct-horse-battery" } });
  fireEvent.click(screen.getByRole("button", { name: "Add user" }));
  expect((await screen.findByRole("alert")).textContent).toMatch(/already in use/);
  expect((screen.getByLabelText("Email") as HTMLInputElement).value).toBe("oncall@example.com");
});

// A role the page does not know would be offered back in the picker as
// something else and could be saved that way.
it("refuses a list it cannot read rather than guessing at roles", async () => {
  mount(twoUsers, () => json({ users: [{ id: 1, email: "x@example.com", role: "owner", created_at: "2026-01-01T00:00:00Z" }] }));
  expect(await screen.findByText("Users unavailable.")).toBeTruthy();
});

// Your own sessions are under Account; this is for another account's.
it("opens another account's sessions under its row, and not on your own", async () => {
  const fetcher = mount(twoUsers, (url) => url === "/api/v1/users/2/sessions" ? json({ sessions: [] }) : undefined);
  await screen.findByText("oncall@example.com");
  expect(screen.queryByRole("button", { name: "Sessions of operator@example.com" })).toBeNull();
  const toggle = screen.getByRole("button", { name: "Sessions of oncall@example.com" });
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(await screen.findByText("Not signed in anywhere.")).toBeTruthy();
  expect(document.getElementById(toggle.getAttribute("aria-controls")!)).toBeTruthy();
  expect(fetcher.mock.calls.some(([url]) => url === "/api/v1/users/2/sessions")).toBe(true);
  fireEvent.click(toggle);
  expect(screen.queryByText("Not signed in anywhere.")).toBeNull();
});
