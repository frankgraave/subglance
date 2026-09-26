// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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

it("lists every account with its role, and marks your own", async () => {
  mount();
  expect(await screen.findByText("oncall@example.com")).toBeTruthy();
  expect(screen.getByText("2 accounts")).toBeTruthy();
  expect((screen.getByLabelText("Role for oncall@example.com") as HTMLSelectElement).value).toBe("viewer");
  expect(screen.getByText("you")).toBeTruthy();
});

// The one administrator on the page is the one person who could not undo
// demoting or removing themselves, so their own row offers neither.
it("offers no role change and no removal on your own row", async () => {
  mount();
  await screen.findByText("oncall@example.com");
  expect(screen.queryByLabelText("Role for operator@example.com")).toBeNull();
  expect(screen.queryByRole("button", { name: "Remove operator@example.com" })).toBeNull();
  expect(screen.getByRole("button", { name: "Remove oncall@example.com" })).toBeTruthy();
});

it("changes a role only when the change is saved", async () => {
  const fetcher = mount();
  const select = await screen.findByLabelText("Role for oncall@example.com");
  fireEvent.change(select, { target: { value: "editor" } });
  expect(calls(fetcher, "PATCH")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Save role for oncall@example.com" }));
  expect(await screen.findByText("oncall@example.com is now an editor.")).toBeTruthy();
  const [url, init] = calls(fetcher, "PATCH")[0];
  expect(url).toBe("/api/v1/users/2");
  expect(JSON.parse(String((init as RequestInit).body))).toEqual({ role: "editor" });
  await waitFor(() => expect((screen.getByLabelText("Role for oncall@example.com") as HTMLSelectElement).value).toBe("editor"));
  expect(screen.queryByRole("button", { name: "Save role for oncall@example.com" })).toBeNull();
});

it("puts a draft role back with Undo, without asking the server", async () => {
  const fetcher = mount();
  const select = await screen.findByLabelText("Role for oncall@example.com") as HTMLSelectElement;
  fireEvent.change(select, { target: { value: "admin" } });
  fireEvent.click(screen.getByRole("button", { name: "Undo" }));
  expect(select.value).toBe("viewer");
  expect(calls(fetcher, "PATCH")).toHaveLength(0);
});

it("shows the server's refusal on the row it is about", async () => {
  mount(twoUsers, (_url, init) => init?.method === "PATCH"
    ? json({ error: "cannot demote the last administrator", field: "role" }, 400) : undefined);
  fireEvent.change(await screen.findByLabelText("Role for oncall@example.com"), { target: { value: "editor" } });
  fireEvent.click(screen.getByRole("button", { name: "Save role for oncall@example.com" }));
  expect((await screen.findByRole("alert")).textContent).toBe("cannot demote the last administrator");
});

it("removes an account only after a second, named confirmation", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Remove oncall@example.com" }));
  expect(calls(fetcher, "DELETE")).toHaveLength(0);
  expect(screen.getByText(/revokes their API tokens/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Keep" }));
  expect(calls(fetcher, "DELETE")).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Remove oncall@example.com" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm removing oncall@example.com" }));
  expect(await screen.findByText("oncall@example.com was removed.")).toBeTruthy();
  expect(calls(fetcher, "DELETE")[0][0]).toBe("/api/v1/users/2");
  await waitFor(() => expect(screen.queryByText("oncall@example.com")).toBeNull());
  expect(screen.getByText("1 account")).toBeTruthy();
});

it("adds a viewer by default, and waits for a password of the minimum length", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add user" }));
  const form = screen.getByRole("form", { name: "Add user" });
  expect((screen.getByLabelText("Role") as HTMLSelectElement).value).toBe("viewer");
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
  expect(await screen.findByLabelText("Role for new@example.com")).toBeTruthy();
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
