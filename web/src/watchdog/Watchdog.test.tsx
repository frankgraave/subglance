// @vitest-environment jsdom
import { cleanup, render, screen, fireEvent, waitFor, act } from "@testing-library/react";
import { ShellSlots } from "../shell/ShellSlots";
import { setTopbarSlot, setToolbarSlot } from "../shell/topbarSlot";
import { watchdogKey } from "./api";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { Settings } from "../settings/Settings";

const success = {
 configured: true, interval_seconds: 300, last_decision_at: "2026-09-20T09:00:00Z",
 last_attempt_at: "2026-09-20T09:00:00Z", last_success_at: "2026-09-20T09:00:01Z",
 last_result: "succeeded", last_status_code: 204, last_event: "alive",
 suppressed: false, in_flight: false, overdue: false,
};
const clients: QueryClient[] = [];
function settings(body: unknown, status = 200) {
 const fetcher = vi.fn().mockImplementation(async () => new Response(JSON.stringify(body), { status }));
 vi.stubGlobal("fetch", fetcher);
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 clients.push(client);
 render(<QueryClientProvider client={client}><ShellSlots /><Settings client={client} /></QueryClientProvider>);
 return { client, fetcher };
}
afterEach(() => { cleanup(); for (const client of clients.splice(0)) client.clear(); setTopbarSlot(null); setToolbarSlot(null); vi.unstubAllGlobals(); });

it.each([
 [{ ...success, last_result: "rejected", last_status_code: 403 }, "Last ping rejected (HTTP 403)."],
 [{ ...success, last_result: "transport_error", last_status_code: null }, "Last ping could not reach the receiver."],
 [{ ...success, last_result: "timeout", last_status_code: null }, "Last ping timed out."],
 [{ ...success, last_result: "canceled", last_status_code: null }, "Last ping was canceled."],
 [{ ...success, last_result: "request_error", last_status_code: null }, "Last ping could not be sent."],
 [{ ...success, suppressed: true }, "Pings withheld: the checking pipeline has not progressed."],
 [{ ...success, overdue: true }, "Watchdog activity is overdue. Past pings do not confirm current operation."],
 [{ ...success, in_flight: true }, "Ping in progress; the outcome is not known yet."],
 [{ ...success, last_event: "stopped" }, "The latest attempt was a clean-shutdown ping."],
 [{ ...success, last_result: null, last_status_code: null, last_event: null, last_success_at: null, last_attempt_at: null, last_decision_at: null }, "Waiting for the first ping; startup does not count as success."],
] as const)("reports actual outcome without inventing current health: %s", async (body, sentence) => {
 settings(body);
 expect(await screen.findByText(sentence)).toBeTruthy();
 if (body.last_success_at !== null) expect(document.querySelector('time[datetime="2026-09-20T09:00:01Z"]')).toBeTruthy();
});

it.each([null, {}, { configured: false }, { ...success, configured: "false" }, { ...success, last_result: "https://secret.test" }, { ...success, last_success_at: "private-url" }, { ...success, last_status_code: "private-url" }, { ...success, interval_seconds: -1 }])("rejects malformed diagnostic data rather than calling it unconfigured: %s", async (body) => {
 settings(body);
 expect(await screen.findByText("Watchdog state unavailable.")).toBeTruthy();
 expect(screen.queryByText("Not configured")).toBeNull();
 expect(document.body.textContent).not.toMatch(/private-url|https:\/\/secret/);
});

it("keeps loading, errors and old cached data distinct from disabled state", async () => {
 const { client, fetcher } = settings(success);
 expect(screen.getByText("Loading watchdog state…")).toBeTruthy();
 await screen.findByText("Configured");
 fetcher.mockImplementation(async () => new Response(JSON.stringify({ error: "secret-receiver-url" }), { status: 503 }));
 await act(() => client.invalidateQueries({ queryKey: watchdogKey }));
 expect(await screen.findByText("Watchdog state unavailable. Showing the last retrieved history.")).toBeTruthy();
 expect(screen.queryByText("Not configured")).toBeNull();
 expect(document.body.textContent).not.toContain("secret-receiver-url");
});

it("searches the watchdog section without unmounting a dirty account form", async () => {
 settings(success); await screen.findByText("Configured");
 const password = screen.getByLabelText("Current password") as HTMLInputElement;
 fireEvent.change(password, { target: { value: "unsaved" } });
 const search = screen.getByRole("searchbox", { name: "Search settings" });
 fireEvent.change(search, { target: { value: "watchdog" } });
 expect(screen.queryByText(/No settings match/)).toBeNull();
 expect(document.getElementById("account")?.hidden).toBe(true);
 expect(document.getElementById("self-monitoring")?.hidden).toBe(false);
 fireEvent.change(search, { target: { value: "no such setting" } });
 expect(await screen.findByText(/No settings match/)).toBeTruthy();
 expect(document.getElementById("self-monitoring")?.hidden).toBe(true);
 fireEvent.change(search, { target: { value: "" } });
 await waitFor(() => expect(document.getElementById("account")?.hidden).toBe(false));
 expect(screen.getByLabelText("Current password")).toBe(password);
 expect(password.value).toBe("unsaved");
});
it("shows configured state and the actual successful ping time in Settings", async () => {
 vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(success))));
 const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
 render(<QueryClientProvider client={client}><Settings client={client} /></QueryClientProvider>);
 expect(await screen.findByRole("heading", { name: "Self-monitoring" })).toBeTruthy();
 expect(await screen.findByText("Configured")).toBeTruthy();
 expect(screen.getByText("Last ping succeeded (HTTP 204).")).toBeTruthy();
 expect(document.querySelector('time[datetime="2026-09-20T09:00:01Z"]')).toBeTruthy();
 expect(fetch).toHaveBeenCalledWith("/api/v1/watchdog", expect.objectContaining({ credentials: "same-origin", cache: "no-store" }));
 client.clear();
});
