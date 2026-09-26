// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ResetInstanceCard } from "./ResetInstance";
import { Settings } from "../settings/Settings";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function mount(response: () => Response) {
  const fetcher = vi.fn().mockImplementation(async () => response());
  vi.stubGlobal("fetch", fetcher);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><ResetInstanceCard /></QueryClientProvider>);
  return fetcher;
}

const button = () => screen.getByRole("button", { name: "Delete all data" }) as HTMLButtonElement;
const field = () => screen.getByLabelText(/to enable the button/) as HTMLInputElement;

it("keeps the button disabled until the phrase matches exactly", () => {
  const fetcher = mount(() => json({}));
  expect(button().disabled).toBe(true);
  for (const near of ["delete all data", "DELETE ALL DATA ", " DELETE ALL DATA", "DELETE ALL"]) {
    fireEvent.change(field(), { target: { value: near } });
    expect(button().disabled).toBe(true);
  }
  fireEvent.submit(button().closest("form")!);
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.change(field(), { target: { value: "DELETE ALL DATA" } });
  expect(button().disabled).toBe(false);
  expect(screen.getByText("The phrase matches. Resetting cannot be undone.")).toBeTruthy();
});

it("sends the typed phrase and reports what was deleted", async () => {
  const fetcher = mount(() => json({ monitors: 4, channels: 1, api_tokens: 2, maintenance_windows: 0 }));
  fireEvent.change(field(), { target: { value: "DELETE ALL DATA" } });
  fireEvent.click(button());
  expect(await screen.findByText(
    "Deleted 4 monitors, 1 notification channel, 0 maintenance windows and 2 API tokens.")).toBeTruthy();
  const [url, init] = fetcher.mock.calls[0] as [string, RequestInit];
  expect(url).toBe("/api/v1/instance/reset");
  expect(init.method).toBe("POST");
  expect(JSON.parse(String(init.body))).toEqual({ confirm: "DELETE ALL DATA" });
  // The field is cleared, so the next visit starts gated again.
  expect(field().value).toBe("");
  expect(button().disabled).toBe(true);
});

it("shows the server's refusal and says nothing was deleted when unreachable", async () => {
  mount(() => json({ error: "this action requires an administrator" }, 403));
  fireEvent.change(field(), { target: { value: "DELETE ALL DATA" } });
  fireEvent.click(button());
  expect((await screen.findByRole("alert")).textContent).toBe("this action requires an administrator");

  cleanup();
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("offline")));
  render(<QueryClientProvider client={new QueryClient()}><ResetInstanceCard /></QueryClientProvider>);
  fireEvent.change(field(), { target: { value: "DELETE ALL DATA" } });
  fireEvent.click(button());
  expect((await screen.findByRole("alert")).textContent).toMatch(/Nothing was deleted/);
});

it("is on the settings page for an administrator only", () => {
  vi.stubGlobal("fetch", vi.fn().mockImplementation(async () => json({})));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { unmount, container } = render(<Settings client={client} canAdmin={false} />);
  // Not mounted at all, rather than hidden: a hidden card is one search
  // query or one stylesheet away from being shown to someone who cannot use it.
  expect(container.querySelector("#reset")).toBeNull();
  unmount();
  render(<Settings client={client} canAdmin />);
  expect(screen.getByRole("heading", { name: "Reset this instance" })).toBeTruthy();
});
