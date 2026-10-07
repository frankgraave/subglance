// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { StatusPagesCard } from "./StatusPages";
import { slugFrom, statusPagesKey, type StatusPage } from "./api";
import { pageMonitors, samplePages } from "./fixtures";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

type Handler = (url: string, init?: RequestInit) => Response | undefined;

/** A small in-memory server for the five admin routes. */
function mount(pages: StatusPage[] = samplePages, onRequest?: Handler) {
  let list = structuredClone(pages);
  let nextId = 10;
  const fetcher = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
    const answer = onRequest?.(url, init);
    if (answer) return answer;
    if (url === "/api/v1/monitors") return json({ monitors: pageMonitors });
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    const slug = decodeURIComponent(url.split("/")[4] ?? "");
    const found = list.find((p) => p.slug === slug);
    if (init?.method === "POST") {
      const created: StatusPage = { ...body, id: nextId++, created_at: "2026-09-29T00:00:00Z", updated_at: "2026-09-29T00:00:00Z", entries: [], unnamed_monitor_ids: [] };
      list = [...list, created];
      return json(created, 201);
    }
    if (init?.method === "PUT" && url.endsWith("/entries") && found) {
      const updated = { ...found, entries: body.entries.map((e: { monitor_id: number; display_name: string }) =>
        ({ ...e, public_key: found.entries.find((old) => old.monitor_id === e.monitor_id)?.public_key ?? `new${e.monitor_id}` })) };
      list = list.map((p) => p.id === found.id ? updated : p);
      return json(updated);
    }
    if (init?.method === "PUT" && found) {
      const updated = { ...found, ...body };
      list = list.map((p) => p.id === found.id ? updated : p);
      return json(updated);
    }
    if (init?.method === "DELETE" && found) {
      list = list.filter((p) => p.id !== found.id);
      return new Response(null, { status: 204 });
    }
    if (url === "/api/v1/status-pages") return json({ pages: list });
    return json({ error: "status page not found" }, 404);
  });
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  clients.push(client);
  render(<QueryClientProvider client={client}><StatusPagesCard /></QueryClientProvider>);
  return fetcher;
}

const calls = (fetcher: ReturnType<typeof vi.fn>, method: string) =>
  fetcher.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === method);
const sent = (fetcher: ReturnType<typeof vi.fn>, method: string, index = 0) =>
  JSON.parse(String((calls(fetcher, method)[index][1] as RequestInit).body));

it("lists every page with its address, state and what it shows", async () => {
  mount();
  const list = await screen.findByRole("list", { name: "Status pages" });
  expect(screen.getByRole("heading", { name: "Status pages (2)" })).toBeTruthy();
  const rows = within(list).getAllByRole("listitem");
  expect(rows[0].textContent).toContain("/status/status · 1 service · hidden from search engines");
  expect(rows[0].textContent).toContain("published");
  expect(rows[1].textContent).toContain("1 not shown until named");
  expect(rows[1].textContent).toContain("off");
});

it("links to a published page, and not to one that is off", async () => {
  mount();
  const list = await screen.findByRole("list", { name: "Status pages" });
  const [published, off] = within(list).getAllByRole("listitem");
  expect(within(published).getByRole("link", { name: "Open Acme services" }).getAttribute("href")).toBe("/status/status");
  expect(within(off).queryByRole("link")).toBeNull();
});

it("suggests the address from the title until it is typed by hand, and creates the page off", async () => {
  const fetcher = mount([]);
  expect(await screen.findByText("No status pages yet.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "New page" }));
  const form = screen.getByRole("form", { name: "New status page" });
  const submit = within(form).getByRole("button", { name: "Create page" }) as HTMLButtonElement;
  expect(submit.disabled).toBe(true);
  fireEvent.change(within(form).getByLabelText("Title"), { target: { value: "Café Services" } });
  expect((within(form).getByLabelText("Address") as HTMLInputElement).value).toBe("cafe-services");
  expect(form.textContent).toContain("/status/cafe-services");
  fireEvent.change(within(form).getByLabelText("Address"), { target: { value: "cafe" } });
  fireEvent.change(within(form).getByLabelText("Title"), { target: { value: "Café" } });
  expect((within(form).getByLabelText("Address") as HTMLInputElement).value).toBe("cafe");
  expect((within(form).getByRole("checkbox", { name: "Published" }) as HTMLInputElement).checked).toBe(false);
  fireEvent.click(submit);
  // Creating leads straight to choosing what the page shows.
  expect(await screen.findByRole("form", { name: "Services on Café" })).toBeTruthy();
  expect(screen.getByText("Café was created. Add the services it shows, then publish it.")).toBeTruthy();
  const body = sent(fetcher, "POST");
  expect(body).toMatchObject({ slug: "cafe", title: "Café", selection: "monitors", enabled: false, indexable: false, tag_key: "", tag_value: "" });
  expect(body).toMatchObject({ language: "en", accent: "", hide_credit: false });
  expect(Object.keys(body).sort()).toEqual(["accent", "description", "enabled", "hide_credit", "indexable", "language", "selection",
    "slug", "tag_key", "tag_value", "timezone", "title"]);
  // A logo needs a page to belong to; the new-page form says so instead.
  expect(screen.queryByLabelText("Logo (optional)")).toBeNull();
});

// The server replaces every setting and reads an omitted boolean as false,
// so a save that sent only what changed would unpublish the page.
it("sends every setting on an update, so fixing the title keeps the page published", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  fireEvent.change(within(form).getByLabelText("Title"), { target: { value: "Acme status" } });
  // An existing page's address does not follow its title.
  expect((within(form).getByLabelText("Address") as HTMLInputElement).value).toBe("status");
  fireEvent.click(within(form).getByRole("button", { name: "Save settings" }));
  expect(await screen.findByText("Settings for Acme status were saved.")).toBeTruthy();
  expect(calls(fetcher, "PUT")[0][0]).toBe("/api/v1/status-pages/status");
  expect(sent(fetcher, "PUT")).toEqual({
    slug: "status", title: "Acme status", description: "", timezone: "Europe/Amsterdam", selection: "monitors",
    tag_key: "", tag_value: "", indexable: false, enabled: true, language: "en", accent: "", hide_credit: false,
  });
  expect(screen.queryByRole("form", { name: /Settings for/ })).toBeNull();
});

it("warns that shared links break before a published page's address changes", async () => {
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  expect(within(form).queryByText(/stop working/)).toBeNull();
  fireEvent.change(within(form).getByLabelText("Address"), { target: { value: "health" } });
  expect(within(form).getByRole("status").textContent).toBe("Links already shared to /status/status stop working when you save.");
});

it("draws a refusal under the field the server blamed, and keeps the input", async () => {
  mount(samplePages, (_url, init) => init?.method === "PUT" && !String(_url).endsWith("/entries")
    ? json({ error: "another status page already uses this slug", field: "slug" }, 409) : undefined);
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  const slug = within(form).getByLabelText("Address") as HTMLInputElement;
  fireEvent.change(slug, { target: { value: "acme" } });
  fireEvent.click(within(form).getByRole("button", { name: "Save settings" }));
  const alert = await within(form).findByRole("alert");
  expect(alert.textContent).toBe("another status page already uses this slug");
  expect(slug.getAttribute("aria-invalid")).toBe("true");
  expect(slug.getAttribute("aria-describedby")).toContain(alert.id);
  expect(slug.value).toBe("acme");
});

it("clears the tag when a tag page is switched back to chosen monitors", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme for customers" }));
  const form = screen.getByRole("form", { name: "Settings for Acme for customers" });
  expect((within(form).getByLabelText("Tag key") as HTMLInputElement).value).toBe("customer");
  fireEvent.change(within(form).getByLabelText("Shows"), { target: { value: "monitors" } });
  expect(within(form).queryByLabelText("Tag key")).toBeNull();
  fireEvent.click(within(form).getByRole("button", { name: "Save settings" }));
  await waitFor(() => expect(calls(fetcher, "PUT")).toHaveLength(1));
  expect(sent(fetcher, "PUT")).toMatchObject({ selection: "monitors", tag_key: "", tag_value: "" });
});

// Design §1.1: the public name never falls back to the internal one, so the
// editor must not make that fallback one click away either.
it("adds a monitor with an empty public name and will not save it blank", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Services on Acme services" }));
  const form = await screen.findByRole("form", { name: "Services on Acme services" });
  const picker = within(form).getByLabelText("Add a monitor") as HTMLSelectElement;
  fireEvent.change(picker, { target: { value: "2" } });
  fireEvent.click(within(form).getByRole("button", { name: "Add" }));
  const name = within(form).getByLabelText("Public name for checkout-prod") as HTMLInputElement;
  expect(name.value).toBe("");
  expect(document.activeElement).toBe(name);
  fireEvent.click(within(form).getByRole("button", { name: "Save services" }));
  expect(within(form).getByRole("alert").textContent).toBe("Give checkout-prod a public name, or remove it from the page.");
  expect(name.getAttribute("aria-invalid")).toBe("true");
  expect(calls(fetcher, "PUT")).toHaveLength(0);
  fireEvent.change(name, { target: { value: " Checkout " } });
  fireEvent.click(within(form).getByRole("button", { name: "Move checkout-prod up" }));
  fireEvent.click(within(form).getByRole("button", { name: "Save services" }));
  expect(await screen.findByText("Services on Acme services were saved.")).toBeTruthy();
  expect(calls(fetcher, "PUT")[0][0]).toBe("/api/v1/status-pages/status/entries");
  expect(sent(fetcher, "PUT")).toEqual({ entries: [{ monitor_id: 2, display_name: "Checkout" }, { monitor_id: 1, display_name: "API" }] });
  await waitFor(() => expect(screen.getByRole("list", { name: "Status pages" }).textContent).toContain("2 services"));
});

it("offers only tagged monitors on a tag page and names the ones still hidden", async () => {
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Services on Acme for customers" }));
  const form = await screen.findByRole("form", { name: "Services on Acme for customers" });
  expect(within(form).getByRole("status").textContent).toBe("Not shown until named: checkout-prod.");
  const options = [...(within(form).getByLabelText("Add a monitor") as HTMLSelectElement).options].map((o) => o.textContent);
  expect(options).toEqual(["Choose a monitor", "checkout-prod"]);
});

it("removes a service from the draft only, until it is saved", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Services on Acme services" }));
  const form = await screen.findByRole("form", { name: "Services on Acme services" });
  const save = within(form).getByRole("button", { name: "Save services" }) as HTMLButtonElement;
  expect(save.disabled).toBe(true);
  fireEvent.click(within(form).getByRole("button", { name: "Remove api-eu-west-1 from the page" }));
  expect(within(form).getByText("No services on this page yet.")).toBeTruthy();
  expect(calls(fetcher, "PUT")).toHaveLength(0);
  fireEvent.click(save);
  await waitFor(() => expect(calls(fetcher, "PUT")).toHaveLength(1));
  expect(sent(fetcher, "PUT")).toEqual({ entries: [] });
});

it("asks before a drawer with unsaved changes is closed", async () => {
  mount();
  const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  fireEvent.click(within(form).getByRole("button", { name: "Cancel" }));
  // Cancel is an explicit choice; the scrim and Escape are the accidents.
  expect(screen.queryByRole("form", { name: "Settings for Acme services" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Settings for Acme services" }));
  const again = screen.getByRole("form", { name: "Settings for Acme services" });
  fireEvent.change(within(again).getByLabelText("Title"), { target: { value: "Changed" } });
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(confirm).toHaveBeenCalledWith("Discard this unsaved status page? Your changes will be lost.");
  expect(screen.getByRole("form", { name: "Settings for Acme services" })).toBeTruthy();
});

it("deletes a page only after its address is retyped", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Delete Acme services" }));
  expect(screen.getByText(/The monitors and their history stay/)).toBeTruthy();
  const confirm = screen.getByRole("button", { name: "Delete status" });
  fireEvent.change(screen.getByLabelText(/to confirm/i), { target: { value: "Acme services" } });
  expect(confirm.hasAttribute("disabled")).toBe(true);
  fireEvent.change(screen.getByLabelText(/to confirm/i), { target: { value: "status" } });
  fireEvent.click(confirm);
  expect(await screen.findByText("Acme services was deleted.")).toBeTruthy();
  expect(calls(fetcher, "DELETE")[0][0]).toBe("/api/v1/status-pages/status");
  await waitFor(() => expect(screen.getByRole("heading", { name: "Status pages (1)" })).toBeTruthy());
});

// A page deleted elsewhere must not leave its settings open as a new-page
// form: saving that would quietly create a duplicate.
it("closes a page's settings when a refetch no longer lists that page", async () => {
  let gone = false;
  const fetcher = mount(samplePages, (url, init) =>
    gone && url === "/api/v1/status-pages" && !init?.method ? json({ pages: samplePages.slice(1) }) : undefined);
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  expect(screen.getByRole("form", { name: "Settings for Acme services" })).toBeTruthy();
  gone = true;
  await act(() => clients[0].invalidateQueries({ queryKey: statusPagesKey }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(screen.queryByRole("form", { name: "New status page" })).toBeNull();
  expect(calls(fetcher, "POST")).toHaveLength(0);
});

// A page misread as off would be offered back that way and saved.
it("refuses a list it cannot read rather than guessing at a page's state", async () => {
  const { enabled: _, ...broken } = samplePages[0];
  mount(samplePages, (url, init) => url === "/api/v1/status-pages" && !init?.method ? json({ pages: [broken] }) : undefined);
  expect(await screen.findByText("Status pages unavailable.")).toBeTruthy();
});

it("sends the language, the accent and the footer credit with the other settings", async () => {
  const fetcher = mount();
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme for customers" }));
  const form = screen.getByRole("form", { name: "Settings for Acme for customers" });
  expect((within(form).getByLabelText("Language") as HTMLSelectElement).value).toBe("nl");
  const credit = within(form).getByRole("checkbox", { name: /Monitored with SubGlance/ }) as HTMLInputElement;
  // The checkbox says what is shown; the setting stores what is hidden.
  expect(credit.checked).toBe(false);
  fireEvent.click(credit);
  fireEvent.change(within(form).getByLabelText("Language"), { target: { value: "en" } });
  fireEvent.change(within(form).getByLabelText("Accent colour (optional)"), { target: { value: "#2563eb" } });
  fireEvent.click(within(form).getByRole("button", { name: "Save settings" }));
  await waitFor(() => expect(calls(fetcher, "PUT")).toHaveLength(1));
  expect(sent(fetcher, "PUT")).toMatchObject({ language: "en", accent: "#2563eb", hide_credit: false });
});

it("draws the server's contrast refusal under the accent field", async () => {
  const message = "accent #ffff00 measures 13.80:1 against the dark page and 1.03:1 against the light page; the title needs 3.00:1 on both";
  mount(samplePages, (url, init) => init?.method === "PUT" && !String(url).endsWith("/entries")
    ? json({ error: message, field: "accent" }, 400) : undefined);
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  const accent = within(form).getByLabelText("Accent colour (optional)") as HTMLInputElement;
  fireEvent.change(accent, { target: { value: "#ffff00" } });
  fireEvent.click(within(form).getByRole("button", { name: "Save settings" }));
  const alert = await within(form).findByRole("alert");
  expect(alert.textContent).toBe(message);
  expect(accent.getAttribute("aria-invalid")).toBe("true");
  expect(accent.getAttribute("aria-describedby")).toContain(alert.id);
});

it("uploads a chosen logo as its bytes, shows it, and removes it", async () => {
  const fetcher = mount(samplePages, (url, init) => {
    if (!String(url).endsWith("/logo")) return undefined;
    const page = samplePages[0];
    if (init?.method === "PUT") {
      return json({ ...page, logo: { content_type: "image/png", width: 80, height: 20, bytes: 3, path: "/status/logos/aaaaaaaaaaaaaaaa.png", updated_at: "2026-10-07T00:00:00Z" } });
    }
    if (init?.method === "DELETE") return json({ ...page, logo: null });
    return undefined;
  });
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  const file = new File([new Uint8Array([1, 2, 3])], "logo.svg", { type: "image/svg+xml" });
  fireEvent.change(within(form).getByLabelText("Logo (optional)"), { target: { files: [file] } });
  const preview = await within(form).findByRole("img", { name: "Current logo of Acme services" });
  expect(preview.getAttribute("src")).toBe("/api/v1/status-pages/status/logo?v=aaaaaaaaaaaaaaaa.png");
  const [url, init] = calls(fetcher, "PUT")[0] as [string, RequestInit];
  expect(url).toBe("/api/v1/status-pages/status/logo");
  // The server reads the type from the bytes; the form sends neither the
  // name nor the type the browser guessed.
  expect(init.body).toBe(file);
  expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/octet-stream");

  fireEvent.click(within(form).getByRole("button", { name: "Remove logo" }));
  await waitFor(() => expect(within(form).queryByRole("img")).toBeNull());
  expect(calls(fetcher, "DELETE")[0][0]).toBe("/api/v1/status-pages/status/logo");
});

it("refuses a logo over the size limit without uploading it, and shows the server's refusal", async () => {
  const fetcher = mount(samplePages, (url, init) => String(url).endsWith("/logo") && init?.method === "PUT"
    ? json({ error: "the logo must be a PNG, JPEG or WebP image; SVG is not accepted, because it can carry script", field: "logo" }, 400)
    : undefined);
  fireEvent.click(await screen.findByRole("button", { name: "Settings for Acme services" }));
  const form = screen.getByRole("form", { name: "Settings for Acme services" });
  const input = within(form).getByLabelText("Logo (optional)");
  fireEvent.change(input, { target: { files: [new File([new Uint8Array(256 * 1024 + 1)], "big.png")] } });
  expect((await within(form).findByRole("alert")).textContent).toBe("The logo is larger than 256 KB.");
  expect(calls(fetcher, "PUT")).toHaveLength(0);

  fireEvent.change(within(form).getByLabelText("Logo (optional)"), { target: { files: [new File(["<svg/>"], "x.png")] } });
  await waitFor(() => expect(within(form).getByRole("alert").textContent).toContain("SVG is not accepted"));
  expect(within(form).getByLabelText("Logo (optional)").getAttribute("aria-invalid")).toBe("true");
});

it("suggests slugs a person would type", () => {
  expect(slugFrom("Acme Services")).toBe("acme-services");
  expect(slugFrom("  Café & Bar  ")).toBe("cafe-bar");
  expect(slugFrom("---")).toBe("");
  expect(slugFrom("x".repeat(70))).toHaveLength(63);
  expect(slugFrom(`${"a".repeat(62)} b`)).toBe("a".repeat(62));
});
