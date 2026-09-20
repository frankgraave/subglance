// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { readFileSync } from "node:fs";
import { LiveDashboardRoot } from "../live/LiveDashboard";
import { disabledWatchdog } from "./fixtures";

const clients: QueryClient[] = [];
afterEach(() => { cleanup(); clients.splice(0).forEach((client) => client.clear()); vi.unstubAllGlobals(); });
function dashboard(watchdog: unknown, status = 200) {
 vi.stubGlobal("matchMedia", () => ({ matches: false, addEventListener() {}, removeEventListener() {} }));
 const fetcher = vi.fn(async (input: string) => new Response(JSON.stringify(input === "/api/v1/watchdog" ? watchdog : { monitors: [] }), { status: input === "/api/v1/watchdog" ? status : 200 }));
 vi.stubGlobal("fetch", fetcher);
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); clients.push(client);
 const source = () => ({ readyState: 1, onopen: null, onerror: null, onmessage: null, close() {}, addEventListener() {} });
 render(<LiveDashboardRoot client={client} createEventSource={source} />);
 return client;
}
it("renders one quiet dashboard caveat only after explicit disabled data and links to an existing README section", async () => {
 dashboard(disabledWatchdog);
 expect(await screen.findByText(/SubGlance cannot report its own outage/)).toBeTruthy();
 expect(screen.getAllByText(/SubGlance cannot report its own outage/)).toHaveLength(1);
 expect(screen.queryByRole("alert")).toBeNull();
 const link = screen.getByRole("link", { name: "Read about self-monitoring" });
 expect(link.getAttribute("href")).toBe("https://github.com/frankgraave/subglance/blob/develop/README.md#documentation");
 expect(readFileSync("../README.md", "utf8")).toContain("## Documentation");
});
it.each([
 [null, 200], [{}, 200], [{ configured: false }, 200],
 [disabledWatchdog, 503],
 [{ ...disabledWatchdog, configured: true, interval_seconds: 300 }, 200],
])("does not misreport unknown or configured state as disabled", async (body, status) => {
 const client = dashboard(body, status);
 await waitFor(() => expect(client.getQueryState(["watchdog"])?.fetchStatus).toBe("idle"));
 expect(screen.queryByText(/SubGlance cannot report its own outage/)).toBeNull();
});
