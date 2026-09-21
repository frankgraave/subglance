// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorsRoot } from "./LiveMonitors";
import { inventoryFromApi } from "./inventory";
import { ShellSlots } from "../shell/ShellSlots";
import { MonitorsView } from "./MonitorsView";
import { confirmNavigation } from "../shell/leaveGuard";

const monitors = [1, 2, 3].map((id) =>
  inventoryFromApi({
    id,
    name: `site ${id}`,
    type: "http",
    target: "https://example.com",
    enabled: true,
    status: "up",
    interval_s: 60,
    timeout_s: 10,
    created_at: "2026-09-01T00:00:00Z",
    tags: { evn: "prod" },
  }),
);
const etag = `"tags-${"a".repeat(64)}"`;
const counts = { total: 2, changed: 2, unchanged: 0, collisions: 0 };
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

it("has no management surface for a viewer, and clears selection on permission loss", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const fetchMonitors = async () => monitors;
  const view = render(
    <LiveMonitorsRoot
      client={client}
      fetchMonitors={fetchMonitors}
      canWrite={false}
    />,
  );
  await screen.findByText("site 1");
  expect(screen.queryByRole("button", { name: "Manage tags" })).toBeNull();
  expect(screen.queryByRole("checkbox")).toBeNull();
  view.rerender(
    <LiveMonitorsRoot client={client} fetchMonitors={fetchMonitors} canWrite />,
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Select site 1" }));
  fireEvent.click(screen.getByRole("button", { name: "Manage tags" }));
  expect(screen.getByRole("dialog")).toBeTruthy();
  view.rerender(
    <LiveMonitorsRoot
      client={client}
      fetchMonitors={fetchMonitors}
      canWrite={false}
    />,
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  view.rerender(
    <LiveMonitorsRoot client={client} fetchMonitors={fetchMonitors} canWrite />,
  );
  expect(screen.getByText("0 selected")).toBeTruthy();
});

it("closes the actual tag owner on accepted navigation while retaining search and selection", () => {
  render(<><ShellSlots /><MonitorsView monitors={monitors} onTagChange={vi.fn(async () => counts)} /></>);
  const search = screen.getByRole("searchbox", { name: "Search monitors" });
  fireEvent.change(search, { target: { value: "site 2" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Select site 2" }));
  fireEvent.click(screen.getByRole("button", { name: "Manage tags" }));
  const oldDialog = screen.getByRole("dialog");
  fireEvent.change(within(oldDialog).getByRole("textbox", { name: "Tag key" }), { target: { value: "discarded" } });
  act(() => { expect(confirmNavigation()).toBe(true); });
  expect(oldDialog.isConnected).toBe(false);
  expect(screen.getByRole("searchbox", { name: "Search monitors" })).toBe(search);
  expect((search as HTMLInputElement).value).toBe("site 2");
  expect(screen.getByText("1 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Manage tags" }));
  expect((within(screen.getByRole("dialog")).getByRole("textbox", { name: "Tag key" }) as HTMLInputElement).value).toBe("");
});

it("selects only visible rows, preserves hidden selections, and forgets removed rows", () => {
  const props = { monitors, onTagChange: vi.fn(async () => counts) };
  const view = render(
    <>
      <ShellSlots />
      <MonitorsView {...props} />
    </>,
  );
  fireEvent.change(screen.getByRole("searchbox", { name: "Search monitors" }), {
    target: { value: "site 2" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Select all visible (1)" }),
  );
  expect(screen.getByText("1 selected")).toBeTruthy();
  fireEvent.change(screen.getByRole("searchbox", { name: "Search monitors" }), {
    target: { value: "" },
  });
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Select site 1",
      }) as HTMLInputElement
    ).checked,
  ).toBe(false);
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Select site 2",
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
  view.rerender(
    <>
      <ShellSlots />
      <MonitorsView {...props} monitors={[monitors[0], monitors[2]]} />
    </>,
  );
  expect(screen.getByText("0 selected")).toBeTruthy();
  view.rerender(
    <>
      <ShellSlots />
      <MonitorsView {...props} />
    </>,
  );
  expect(
    (
      screen.getByRole("checkbox", {
        name: "Select site 2",
      }) as HTMLInputElement
    ).checked,
  ).toBe(false);
  fireEvent.click(
    screen.getByRole("button", { name: "Select all visible (3)" }),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Select site 2" }));
  expect(screen.getByText("2 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Clear selection" }));
  expect(screen.getByText("0 selected")).toBeTruthy();
  view.unmount();
  render(
    <>
      <ShellSlots />
      <MonitorsView {...props} />
    </>,
  );
  expect(screen.getByText("0 selected")).toBeTruthy();
});

it("renames globally with explicit merge policy and invalidates dashboard and detail caches", async () => {
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity, staleTime: Infinity },
    },
  });
  client.setQueryData(["monitors", 100], [{ tags: { evn: "prod" } }]);
  client.setQueryData(["monitor-detail", "1"], { tags: { evn: "prod" } });
  client.setQueryData(["incidents", "open"], [{ reminder: { status: "scheduled" } }]);
  const request = vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(
      async (_url, init) =>
        new Response(JSON.stringify({ ...counts, collisions: 1 }), {
          headers:
            init?.headers && new Headers(init.headers).has("If-Match")
              ? {}
              : { ETag: etag },
        }),
    );
  render(
    <>
      <ShellSlots />
      <LiveMonitorsRoot client={client} fetchMonitors={async () => monitors} />
    </>,
  );
  await screen.findByRole("checkbox", { name: "Select site 1" });
  fireEvent.click(screen.getByRole("button", { name: "Manage tags" }));
  const dialog = screen.getByRole("dialog");
  fireEvent.change(within(dialog).getByRole("combobox", { name: "Action" }), {
    target: { value: "rename_key" },
  });
  expect(
    within(dialog).getByText(
      /Every matching monitor in this instance, not just the selection/,
    ),
  ).toBeTruthy();
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Tag key" }), {
    target: { value: "evn" },
  });
  fireEvent.change(within(dialog).getByRole("textbox", { name: "New key" }), {
    target: { value: "env" },
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Preview change" }),
  );
  await within(dialog).findByRole("button", { name: "Confirm tag change" });
  expect(
    within(dialog).getByText(
      /1 monitors already have the destination key; their existing value will be kept/,
    ),
  ).toBeTruthy();
  expect(
    within(dialog).getByText(
      /The old key is removed, including on those monitors/,
    ),
  ).toBeTruthy();
  expect(JSON.parse(String(request.mock.calls[0][1]?.body))).toEqual({
    action: "rename_key",
    key: "evn",
    new_key: "env",
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Confirm tag change" }),
  );
  await within(dialog).findByText("Changed 2 monitors; 0 unchanged.");
  expect(client.getQueryState(["monitors", 100])?.isInvalidated).toBe(true);
  expect(client.getQueryState(["monitor-detail", "1"])?.isInvalidated).toBe(
    true,
  );
  expect(client.getQueryState(["incidents", "open"])?.isInvalidated).toBe(true);
  client.clear();
});

it("selects multiple monitors, previews once and atomically applies with a paired validator", async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const fetchMonitors = vi.fn(async () => monitors);
  const request = vi
    .spyOn(globalThis, "fetch")
    .mockResolvedValue(
      new Response(JSON.stringify(counts), { headers: { ETag: etag } }),
    );
  render(
    <>
      <ShellSlots />
      <LiveMonitorsRoot client={client} fetchMonitors={fetchMonitors} />
    </>,
  );
  const select = await screen.findByRole("checkbox", { name: "Select site 1" });
  fireEvent.click(select);
  fireEvent.click(screen.getByRole("checkbox", { name: "Select site 2" }));
  expect(screen.getByText("2 selected")).toBeTruthy();
  fireEvent.change(screen.getByRole("searchbox", { name: "Search monitors" }), {
    target: { value: "site 3" },
  });
  expect(screen.getByText("2 selected · 2 hidden by filters")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Manage tags" }));
  const dialog = screen.getByRole("dialog", { name: "Manage tags" });
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Tag key" }), {
    target: { value: "env" },
  });
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Tag value" }), {
    target: { value: "prod" },
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Preview change" }),
  );
  await within(dialog).findByRole("button", { name: "Confirm tag change" });
  expect(request).toHaveBeenCalledTimes(1);
  expect(JSON.parse(String(request.mock.calls[0][1]?.body))).toEqual({
    action: "apply",
    monitor_ids: [1, 2],
    key: "env",
    value: "prod",
  });
  expect(
    within(dialog).getByText(/Existing values for this key will be replaced/),
  ).toBeTruthy();
  request.mockResolvedValueOnce(new Response(JSON.stringify(counts)));
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Confirm tag change" }),
  );
  await within(dialog).findByText("Changed 2 monitors; 0 unchanged.");
  expect(request).toHaveBeenCalledTimes(2);
  expect(request.mock.calls[1][0]).toBe("/api/v1/monitors/tags");
  expect(new Headers(request.mock.calls[1][1]?.headers).get("If-Match")).toBe(
    etag,
  );
  await waitFor(() => expect(fetchMonitors).toHaveBeenCalledTimes(2));
});
