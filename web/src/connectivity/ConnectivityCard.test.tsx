// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ConnectivityCard } from "./ConnectivityCard";
import { parseTargets } from "./settings";
import { connectivityKey } from "./api";
import { defaultConnectivitySettings, pinnedConnectivitySettings } from "./fixtures";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); });

const URL = "/api/v1/settings/connectivity";

function json(body: unknown, status = 200, etag?: string) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json", ...(etag ? { etag } : {}) } });
}

type Handler = (init?: RequestInit) => Response | Promise<Response>;

function mount(read: unknown, onPut?: Handler) {
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
    if (url !== URL) throw new Error(`Unexpected request: ${url}`);
    if (init?.method === "PUT") return onPut ? onPut(init) : json(read, 200, 'W/"2"');
    return json(read, 200, 'W/"1"');
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><ConnectivityCard /></QueryClientProvider>);
  return { fetcher, client };
}

const puts = (fetcher: ReturnType<typeof vi.fn>) => fetcher.mock.calls.filter(([, init]) => init?.method === "PUT");
const targetsBox = () => screen.findByLabelText("Addresses to dial") as Promise<HTMLTextAreaElement>;
const toggle = () => screen.getByRole("checkbox", { name: /Check this host's own connection/ }) as HTMLInputElement;
const save = () => screen.getByRole("button", { name: "Save connectivity check" }) as HTMLButtonElement;

it("shows the switch and the addresses in force, and says the check is outbound traffic", async () => {
  mount(defaultConnectivitySettings);
  expect((await targetsBox()).value).toBe("1.1.1.1:53\n9.9.9.9:53");
  expect(toggle().checked).toBe(true);
  expect(screen.getByText(/This is outbound traffic/)).toBeTruthy();
  // Nothing changed yet, and the defaults are what is in force.
  expect(save().disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Restore default addresses" }) as HTMLButtonElement).disabled).toBe(true);
});

it("saves only what changed, conditional on the version it read, and applies it at once", async () => {
  const saved = {
    ...defaultConnectivitySettings,
    targets: { value: ["gateway:443"], source: "database", pinned_by: null },
  };
  const { fetcher, client } = mount(defaultConnectivitySettings, () => json(saved, 200, 'W/"2"'));
  const invalidate = vi.spyOn(client, "invalidateQueries");
  fireEvent.change(await targetsBox(), { target: { value: "  gateway:443 \n\n" } });
  expect(save().disabled).toBe(false);
  fireEvent.click(save());
  expect(await screen.findByText("Saved. The check uses these settings now.")).toBeTruthy();
  const [[, init]] = puts(fetcher);
  // The switch did not change, so it is not sent; the list is trimmed.
  expect(JSON.parse(String(init.body))).toEqual({ targets: ["gateway:443"] });
  expect((init.headers as Record<string, string>)["If-Match"]).toBe('W/"1"');
  // The form now holds what the server answered, and the dashboard's offline
  // line is asked again, because a new list resets it.
  expect((await targetsBox()).value).toBe("gateway:443");
  expect(invalidate).toHaveBeenCalledWith({ queryKey: connectivityKey });
});

it("turns the check off without sending the list", async () => {
  const { fetcher } = mount(defaultConnectivitySettings, () => json({
    ...defaultConnectivitySettings, enabled: { value: false, source: "database", pinned_by: null },
  }, 200, 'W/"2"'));
  await targetsBox();
  fireEvent.click(toggle());
  expect(screen.getByText(/every monitor is reported down/)).toBeTruthy();
  fireEvent.click(save());
  await screen.findByText("Saved. The check uses these settings now.");
  expect(JSON.parse(String(puts(fetcher)[0][1].body))).toEqual({ enabled: false });
  expect(toggle().checked).toBe(false);
});

it("restores the default addresses into the field without saving them", async () => {
  const { fetcher } = mount({
    ...defaultConnectivitySettings, targets: { value: ["gateway:443"], source: "database", pinned_by: null },
  });
  expect((await targetsBox()).value).toBe("gateway:443");
  fireEvent.click(screen.getByRole("button", { name: "Restore default addresses" }));
  expect((await targetsBox()).value).toBe("1.1.1.1:53\n9.9.9.9:53");
  expect(puts(fetcher)).toHaveLength(0);
  expect(save().disabled).toBe(false);
});

it("puts the server's refusal under the field it names", async () => {
  mount(defaultConnectivitySettings, () => json({
    error: "connectivity target \"gateway\": want host:port", field: "targets",
  }, 400));
  const box = await targetsBox();
  fireEvent.change(box, { target: { value: "gateway" } });
  fireEvent.click(save());
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("want host:port");
  expect(box.getAttribute("aria-invalid")).toBe("true");
  expect(box.getAttribute("aria-describedby")?.split(" ")).toContain(alert.id);
  // Typing again clears it.
  fireEvent.change(box, { target: { value: "gateway:443" } });
  expect(screen.queryByRole("alert")).toBeNull();
  expect(box.getAttribute("aria-invalid")).toBeNull();
});

it("reloads instead of overwriting when someone else saved first", async () => {
  let reads = 0;
  const newer = { ...defaultConnectivitySettings, targets: { value: ["router:80"], source: "database", pinned_by: null } };
  const fetcher = vi.fn().mockImplementation(async (_url: string, init?: RequestInit) => {
    if (init?.method === "PUT") return json({ error: "changed by someone else" }, 412);
    reads += 1;
    return json(reads === 1 ? defaultConnectivitySettings : newer, 200, `W/"${reads}"`);
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><ConnectivityCard /></QueryClientProvider>);
  fireEvent.change(await targetsBox(), { target: { value: "gateway:443" } });
  fireEvent.click(save());
  expect(await screen.findByText(/changed by someone else while you were editing/)).toBeTruthy();
  await waitFor(async () => expect((await targetsBox()).value).toBe("router:80"));
  expect(reads).toBe(2);
});

it("shows pinned settings read-only, naming what fixed them, and offers no save", async () => {
  const { fetcher } = mount(pinnedConnectivitySettings);
  const box = await targetsBox();
  expect(box.value).toBe("gateway:443\nbackup-host:22");
  expect(box.disabled).toBe(true);
  expect(toggle().disabled).toBe(true);
  expect(screen.getByText("Set by SUBGLANCE_CONNECTIVITY_CHECK to on; change it there.")).toBeTruthy();
  expect(screen.getByText("Set by SUBGLANCE_CONNECTIVITY_TARGETS; change it there.")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Save connectivity check" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Restore default addresses" })).toBeNull();
  expect(puts(fetcher)).toHaveLength(0);
});

it("never sends a pinned field when the other one changes", async () => {
  const { fetcher } = mount({ ...pinnedConnectivitySettings, enabled: defaultConnectivitySettings.enabled }, () =>
    json({ ...pinnedConnectivitySettings, enabled: { value: false, source: "database", pinned_by: null } }, 200, 'W/"2"'));
  await targetsBox();
  fireEvent.click(toggle());
  fireEvent.click(save());
  await screen.findByText("Saved. The check uses these settings now.");
  expect(JSON.parse(String(puts(fetcher)[0][1].body))).toEqual({ enabled: false });
});

it("says the settings are unavailable rather than guessing at an off-shape answer", async () => {
  mount({ ...defaultConnectivitySettings, targets: { value: "1.1.1.1:53", source: "default", pinned_by: null } });
  expect(await screen.findByText("Connectivity settings unavailable.")).toBeTruthy();
  expect(screen.queryByLabelText("Addresses to dial")).toBeNull();
});

it("reads one address per line, and a pasted comma-separated list as well", () => {
  expect(parseTargets("a:1\n\n  b:2  \n")).toEqual(["a:1", "b:2"]);
  expect(parseTargets("a:1, b:2,")).toEqual(["a:1", "b:2"]);
  expect(parseTargets("[2001:db8::1]:53")).toEqual(["[2001:db8::1]:53"]);
  expect(parseTargets("   ")).toEqual([]);
});
