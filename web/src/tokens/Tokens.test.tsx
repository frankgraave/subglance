// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { TokensCard } from "./Tokens";
import { rolesWithin } from "./api";
import { sampleTokens } from "./fixtures";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

const created = {
  token: "sgp_secretvalue0123456789",
  warning: "This is the only time the token is shown. Store it now; it cannot be recovered.",
  details: { id: 9, name: "grafana-2", prefix: "sgp_secret", role: "viewer", created_at: "2026-09-26T08:00:00Z" },
};

function mount(role = "admin", onRequest?: (url: string, init?: RequestInit) => Response | undefined) {
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) =>
    onRequest?.(url, init) ?? (init?.method === "POST" ? json(created, 201)
      : init?.method === "DELETE" ? new Response(null, { status: 204 })
      : json({ tokens: sampleTokens })));
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><TokensCard role={role} /></QueryClientProvider>);
  return fetcher;
}

it("lists tokens by name and prefix, with their role and state", async () => {
  mount();
  const list = await screen.findByRole("list", { name: "Your API tokens" });
  const rows = within(list).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(within(rows[0]).getByText("grafana")).toBeTruthy();
  expect(within(rows[0]).getByText("sgp_2c77ab…")).toBeTruthy();
  expect(within(rows[0]).getByText("viewer")).toBeTruthy();
  expect(within(rows[0]).getByText(/never used/)).toBeTruthy();
  // A revoked token stays listed for the record, without a Revoke button.
  expect(within(rows[2]).getByText("revoked")).toBeTruthy();
  expect(within(rows[2]).queryByRole("button")).toBeNull();
  expect(screen.getByText("2 active")).toBeTruthy();
});

it("creates a viewer token by default and shows the secret once, until dismissed", async () => {
  const fetcher = mount();
  fireEvent.change(await screen.findByLabelText("Token name"), { target: { value: "grafana-2" } });
  expect((screen.getByLabelText("Role") as HTMLSelectElement).value).toBe("viewer");
  fireEvent.click(screen.getByRole("button", { name: "Create token" }));

  const secret = await screen.findByLabelText("Token for grafana-2") as HTMLInputElement;
  expect(secret.value).toBe(created.token);
  expect(screen.getByRole("alert").textContent).toMatch(/only time this token is shown/);
  const post = fetcher.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "POST");
  expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({ name: "grafana-2", role: "viewer" });

  // The list refetching does not take the secret away; only the button does.
  await waitFor(() => expect(fetcher.mock.calls.filter(([url, init]) => String(url) === "/api/v1/tokens" && !(init as RequestInit | undefined)?.method).length).toBeGreaterThan(1));
  // Nor does time: a secret on a timer is a secret lost to a coffee break.
  await new Promise((resolve) => setTimeout(resolve, 300));
  expect(screen.getByLabelText("Token for grafana-2")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "I have saved it" }));
  expect(screen.queryByLabelText("Token for grafana-2")).toBeNull();
  expect(screen.queryByDisplayValue(created.token)).toBeNull();
});

it("sends the chosen role and expiry", async () => {
  const fetcher = mount();
  fireEvent.change(await screen.findByLabelText("Token name"), { target: { value: "deploy" } });
  fireEvent.change(screen.getByLabelText("Role"), { target: { value: "editor" } });
  fireEvent.change(screen.getByLabelText("Expires"), { target: { value: "2160h" } });
  expect(screen.getByText(/Right for a deploy pipeline/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Create token" }));
  await screen.findByLabelText("Token for grafana-2");
  const post = fetcher.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "POST");
  expect(JSON.parse(String((post![1] as RequestInit).body))).toEqual({ name: "deploy", role: "editor", expires_in: "2160h" });
});

it("never offers a role above the account's own", () => {
  expect(rolesWithin("admin")).toEqual(["viewer", "editor", "admin"]);
  expect(rolesWithin("editor")).toEqual(["viewer", "editor"]);
  expect(rolesWithin("viewer")).toEqual(["viewer"]);
  expect(rolesWithin("somethingnew")).toEqual(["viewer"]);
});

it("an editor cannot pick admin", async () => {
  mount("editor");
  const select = await screen.findByLabelText("Role") as HTMLSelectElement;
  expect([...select.options].map((o) => o.value)).toEqual(["viewer", "editor"]);
});

it("a viewer lists and revokes but is not offered a create form", async () => {
  mount("viewer");
  await screen.findByRole("list", { name: "Your API tokens" });
  expect(screen.queryByRole("form", { name: "Create API token" })).toBeNull();
  expect(screen.getByText(/can list and revoke its own tokens but not create one/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Revoke grafana" })).toBeTruthy();
});

it("asks once more before revoking, then revokes that token", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Revoke ci-deploy" }));
  expect(fetcher.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === "DELETE")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Confirm revoking ci-deploy" }));
  await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) =>
    String(url) === "/api/v1/tokens/2" && (init as RequestInit | undefined)?.method === "DELETE")).toBe(true));
});

it("shows the server's reason when a create is refused", async () => {
  mount("admin", (_url, init) => init?.method === "POST"
    ? json({ error: "a token cannot have a higher role than your own", field: "role" }, 403) : undefined);
  fireEvent.change(await screen.findByLabelText("Token name"), { target: { value: "x" } });
  fireEvent.click(screen.getByRole("button", { name: "Create token" }));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toBe("a token cannot have a higher role than your own");
  expect(screen.queryByLabelText(/Token for/)).toBeNull();
  // A role refusal sits under the role select and is tied to it, once.
  const select = screen.getByLabelText("Role");
  expect(alert.parentElement).toBe(select.parentElement);
  expect(select.getAttribute("aria-invalid")).toBe("true");
  expect(select.getAttribute("aria-describedby")?.split(" ")).toContain(alert.id);
  expect(screen.getAllByRole("alert")).toHaveLength(1);
});

it("sorts an expired token after the live ones", async () => {
  const expired = { id: 4, name: "old-ci", prefix: "sgp_0ld0ld", role: "editor", created_at: "2020-01-01T00:00:00Z", expires_at: "2020-02-01T00:00:00Z" };
  mount("admin", (url, init) => String(url) === "/api/v1/tokens" && !init?.method
    ? json({ tokens: [expired, ...sampleTokens] }) : undefined);
  const list = await screen.findByRole("list", { name: "Your API tokens" });
  const names = within(list).getAllByRole("listitem").map((row) => within(row).getByText(/^(grafana|ci-deploy|laptop|old-ci)$/).textContent);
  expect(names.slice(0, 2).sort()).toEqual(["ci-deploy", "grafana"]);
  expect(names.slice(2).sort()).toEqual(["laptop", "old-ci"]);
  expect(screen.getByText("2 active")).toBeTruthy();
});

it("refuses a malformed list rather than drawing it", async () => {
  mount("admin", (url, init) => String(url) === "/api/v1/tokens" && !init?.method
    ? json({ tokens: [{ id: 1, name: "x", prefix: "sgp_x", created_at: "2026-01-01T00:00:00Z" }] }) : undefined);
  expect(await screen.findByText("API tokens unavailable.")).toBeTruthy();
});
