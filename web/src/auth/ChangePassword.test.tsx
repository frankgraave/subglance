// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ChangePassword } from "./ChangePassword";
import { onUnauthorized } from "../api/http";
import { MIN_PASSWORD_LENGTH, changePassword } from "./api";

const next = "a fresh long passphrase";
const json = (body: unknown, status: number) => new Response(JSON.stringify(body), {
  status, headers: { "Content-Type": "application/json" },
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });
function fill(current = "old passphrase") {
  fireEvent.change(screen.getByLabelText("Current password"), { target: { value: current } });
  fireEvent.change(screen.getByLabelText("New password"), { target: { value: next } });
  fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: next } });
}
it("uses the real password fetcher, keeps a wrong current password on its field, and retries without ending this session", async () => {
  const unauthorized = vi.fn();
  const stop = onUnauthorized(unauthorized);
  const fetcher = vi.fn().mockResolvedValueOnce(json({ error: "current password is incorrect" }, 401))
    .mockResolvedValueOnce(new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", fetcher);
  render(<ChangePassword />);
  fill("wrong");
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  const error = await screen.findByRole("alert");
  const current = screen.getByLabelText("Current password") as HTMLInputElement;
  expect(error.textContent).toBe("current password is incorrect");
  expect(current.getAttribute("aria-invalid")).toBe("true");
  expect(current.getAttribute("aria-describedby")).toContain(error.id);
  expect(current.parentElement?.contains(error)).toBe(true);
  expect(unauthorized).not.toHaveBeenCalled();
  fill();
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  await screen.findByText(/Every other session was signed out/);
  expect(fetcher).toHaveBeenLastCalledWith("/api/v1/auth/password", expect.objectContaining({
    method: "POST", credentials: "same-origin", body: JSON.stringify({ current_password: "old passphrase", new_password: next }),
    headers: expect.objectContaining({ "Content-Type": "application/json" }), signal: expect.any(AbortSignal),
  }));
  expect(current.value).toBe("");
  expect((screen.getByLabelText("New password") as HTMLInputElement).value).toBe("");
  expect((screen.getByLabelText("Confirm new password") as HTMLInputElement).value).toBe("");
  expect(screen.getByText(/You are still signed in on this tab/)).toBeTruthy();
  expect(unauthorized).not.toHaveBeenCalled();
  stop();
});

it("requires all three passwords, checks shared code-point length and confirmation before requesting", async () => {
  const fetcher = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
  vi.stubGlobal("fetch", fetcher);
  render(<ChangePassword />);
  const current = screen.getByLabelText("Current password") as HTMLInputElement;
  const fresh = screen.getByLabelText("New password") as HTMLInputElement;
  const confirmation = screen.getByLabelText("Confirm new password") as HTMLInputElement;
  for (const field of [current, fresh, confirmation]) expect(field.required).toBe(true);
  expect(current.autocomplete).toBe("current-password");
  expect(fresh.autocomplete).toBe("new-password");
  expect(confirmation.autocomplete).toBe("new-password");
  const submit = () => fireEvent.submit(screen.getByRole("form", { name: "Change password" }));
  submit();
  expect(fetcher).not.toHaveBeenCalled();
  fill(""); submit();
  expect(fetcher).not.toHaveBeenCalled();
  fill();
  for (const value of ["", "x".repeat(MIN_PASSWORD_LENGTH - 1), "🦉".repeat(MIN_PASSWORD_LENGTH - 1)]) {
    fireEvent.change(fresh, { target: { value } });
    fireEvent.change(confirmation, { target: { value } });
    submit();
    expect(fetcher).not.toHaveBeenCalled();
  }
  fireEvent.change(fresh, { target: { value: next } });
  fireEvent.change(confirmation, { target: { value: "" } });
  submit();
  expect(fetcher).not.toHaveBeenCalled();
  fireEvent.change(confirmation, { target: { value: "does not match" } });
  submit();
  expect(fetcher).not.toHaveBeenCalled();
  expect(screen.getByRole("alert").textContent).toMatch(/do not match/);
  fireEvent.change(fresh, { target: { value: "🦉".repeat(MIN_PASSWORD_LENGTH) } });
  fireEvent.change(confirmation, { target: { value: fresh.value } });
  submit();
  await screen.findByText(/Every other session was signed out/);
  expect(fetcher).toHaveBeenCalledTimes(1);
});

it("locks duplicate submits and aborts a pending request on unmount without accepting its late result", async () => {
  let resolve!: (response: Response) => void;
  const fetcher = vi.fn(() => new Promise<Response>((r) => { resolve = r; }));
  vi.stubGlobal("fetch", fetcher);
  const view = render(<ChangePassword />);
  fill();
  fireEvent.submit(screen.getByRole("form", { name: "Change password" }));
  fireEvent.submit(screen.getByRole("form", { name: "Change password" }));
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByLabelText("New password") as HTMLInputElement).disabled).toBe(true);
  const signal = (vi.mocked(fetch).mock.calls[0][1]!).signal!;
  view.unmount();
  expect(signal.aborted).toBe(true);
  resolve(new Response(null, { status: 204 }));
  await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
});

it.each([
  [400, { error: "choose a different password", field: "new_password" }, "new_password"],
  [400, { error: "current rejected", field: "current_password" }, "current_password"],
  [500, { error: "could not change the password" }, null],
  [400, { error: "unsupported field", field: "unknown" }, null],
  [401, { error: "authentication required" }, null],
] as const)("renders real HTTP %s rejections and only expires genuinely unauthorized sessions", async (status, body, field) => {
  const unauthorized = vi.fn(); const stop = onUnauthorized(unauthorized);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json(body, status)));
  render(<ChangePassword />); fill();
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  const error = await screen.findByRole("alert");
  expect(error.textContent).toContain(body.error);
  expect(document.querySelector('input[aria-invalid="true"]')?.getAttribute("name") ?? null).toBe(field);
  expect(unauthorized).toHaveBeenCalledTimes(status === 401 ? 1 : 0);
  expect((screen.getByLabelText("New password") as HTMLInputElement).value).toBe(next);
  expect((screen.getByRole("button") as HTMLButtonElement).disabled).toBe(false);
  stop();
});
it("does not expire a newer session from an aborted password request's late 401", async () => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>((r) => { resolve = r; })));
  const unauthorized = vi.fn(); const stop = onUnauthorized(unauthorized);
  try {
    const controller = new AbortController();
    const result = changePassword("current", next, controller.signal).catch((error) => error);
    controller.abort();
    resolve(json({ error: "authentication required" }, 401));
    expect((await result).name).toBe("AbortError");
    expect(unauthorized).not.toHaveBeenCalled();
  } finally { stop(); }
});

it("reports a network failure without disclosing passwords or claiming a save", async () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
  const storage = vi.spyOn(Storage.prototype, "setItem");
  render(<ChangePassword />); fill();
  fireEvent.click(screen.getByRole("button", { name: "Change password" }));
  expect((await screen.findByRole("alert")).textContent).toMatch(/Could not reach SubGlance/);
  expect(screen.queryByText(/Every other session was signed out/)).toBeNull();
  expect(storage).not.toHaveBeenCalled();
});
