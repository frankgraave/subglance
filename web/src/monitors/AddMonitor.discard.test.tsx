// @vitest-environment jsdom
import { useState } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Drawer } from "../components/Drawer";
import { AddMonitor } from "./AddMonitor";

function Surface() {
  const [open, setOpen] = useState(false);
  return <><button onClick={() => setOpen(true)}>Add monitor</button>
    <Drawer open={open} onClose={() => setOpen(false)} title="Add monitor">
      <AddMonitor onCancel={() => setOpen(false)} onCreated={() => setOpen(false)} />
    </Drawer></>;
}
beforeEach(() => { vi.spyOn(window, "confirm").mockReturnValue(false); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it.each(["Escape", "Close", "Cancel", "backdrop"])("retains a dirty form after cancelling %s, discards only after confirmation, and restores focus", (path) => {
  render(<Surface />);
  const opener = screen.getByRole("button", { name: "Add monitor" });
  opener.focus(); fireEvent.click(opener);
  const input = screen.getByLabelText("Name") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "unsaved name" } }); input.focus();
  const dismiss = () => {
    if (path === "Escape") fireEvent.keyDown(input, { key: "Escape" });
    else if (path === "backdrop") fireEvent.click(document.querySelector(".drawer-scrim")!);
    else fireEvent.click(screen.getByRole("button", { name: path }));
  };
  dismiss();
  expect(window.confirm).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("Name")).toBe(input);
  expect(input.value).toBe("unsaved name");
  vi.mocked(window.confirm).mockReturnValue(true);
  dismiss();
  expect(window.confirm).toHaveBeenCalledTimes(2);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.activeElement).toBe(opener);
  fireEvent.click(opener);
  expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("");
});

it("does not prompt for pristine controls or a reverted edit", () => {
  render(<Surface />); fireEvent.click(screen.getByRole("button", { name: "Add monitor" }));
  fireEvent.click(screen.getByText("Advanced options"));
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  expect(window.confirm).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Add monitor" }));
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "temporary" } });
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(window.confirm).not.toHaveBeenCalled();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("guards browser unload for advanced-only edits without persisting a draft", () => {
  const storage = vi.spyOn(Storage.prototype, "setItem");
  render(<Surface />); fireEvent.click(screen.getByRole("button", { name: "Add monitor" }));
  fireEvent.change(screen.getByLabelText("Give up after"), { target: { value: "15" } });
  const event = new Event("beforeunload", { cancelable: true });
  window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true);
  expect(storage).not.toHaveBeenCalled();
  cleanup();
  const after = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(after);
  expect(after.defaultPrevented).toBe(false);
});

function fillExplicit() {
  fireEvent.change(screen.getByLabelText("What should be watched"), { target: { value: "https://example.test" } });
  fireEvent.change(screen.getByLabelText("Check type"), { target: { value: "http" } });
}
it("resets a successful create even when the caller keeps the form mounted", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 9 }), { status: 201 })));
  render(<AddMonitor />); fillExplicit();
  fireEvent.click(screen.getByRole("button", { name: "Save monitor" }));
  await waitFor(() => expect((screen.getByLabelText("What should be watched") as HTMLInputElement).value).toBe(""));
  const event = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(false);
});
it("aborts pending work on confirmed discard and ignores a late save result", async () => {
  let resolve!: (response: Response) => void;
  const fetcher = vi.fn(() => new Promise<Response>((r) => { resolve = r; }));
  vi.stubGlobal("fetch", fetcher);
  const created = vi.fn();
  const view = render(<AddMonitor onCreated={created} />); fillExplicit();
  const form = screen.getByRole("form", { name: "Add a monitor" });
  fireEvent.submit(form); fireEvent.submit(form);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect((screen.getByLabelText("What should be watched") as HTMLInputElement).matches(":disabled")).toBe(true);
  const signal = vi.mocked(fetch).mock.calls[0][1]!.signal!;
  view.unmount(); expect(signal.aborted).toBe(true);
  resolve(new Response(JSON.stringify({ id: 9 }), { status: 201 }));
  await waitFor(() => expect(created).not.toHaveBeenCalled());
});
