// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { BulkTagDrawer, type TagChange } from "./BulkTagDrawer";
const etag = `"tags-${"a".repeat(64)}"`;
const result = { total: 2, changed: 1, unchanged: 1, collisions: 0, etag };
afterEach(cleanup);
it.each(["key", "value", "selection"])(
  "invalidates a completed preview when %s changes",
  async (field) => {
    const change = vi.fn<TagChange>(async () => result);
    const props = {
      selectedIds: ["1", "2"],
      onChange: change,
      onClose: vi.fn(),
    };
    const view = render(<BulkTagDrawer {...props} />);
    fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
      target: { value: "env" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Tag value" }), {
      target: { value: "prod" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
    await screen.findByRole("button", { name: "Confirm tag change" });
    if (field === "selection")
      view.rerender(<BulkTagDrawer {...props} selectedIds={["2"]} />);
    else
      fireEvent.change(
        screen.getByRole("textbox", {
          name: field === "key" ? "Tag key" : "Tag value",
        }),
        { target: { value: "changed" } },
      );
    expect(
      screen.queryByRole("button", { name: "Confirm tag change" }),
    ).toBeNull();
  },
);

it("blocks duplicate submissions and dismissal while pending, then shows the failure without success", async () => {
  let fail!: (reason: Error) => void;
  const change = vi.fn<TagChange>(
    () =>
      new Promise((_resolve, reject) => {
        fail = reject;
      }),
  );
  const close = vi.fn();
  render(
    <BulkTagDrawer selectedIds={["1"]} onChange={change} onClose={close} />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
    target: { value: "env" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Tag value" }), {
    target: { value: "prod" },
  });
  const form = screen
    .getByRole("button", { name: "Preview change" })
    .closest("form")!;
  fireEvent.submit(form);
  fireEvent.submit(form);
  expect(change).toHaveBeenCalledOnce();
  expect(document.activeElement).toBe(screen.getByText("Working…"));
  expect(screen.getByRole("group").hasAttribute("disabled")).toBe(true);
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(close).not.toHaveBeenCalled();
  fail(new Error("tags changed; preview again"));
  await screen.findByRole("alert");
  expect(document.activeElement).toBe(screen.getByRole("alert"));
  expect(screen.getByRole("alert").textContent).toBe(
    "tags changed; preview again",
  );
  expect(screen.queryByText(/Changed .* monitors/)).toBeNull();
  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
  expect(close).toHaveBeenCalledOnce();
});

it("does not allow committing an empty preview and explains an oversized selection", async () => {
  const change = vi.fn<TagChange>(async () => ({
    total: 1,
    changed: 0,
    unchanged: 1,
    collisions: 0,
    etag,
  }));
  const view = render(
    <BulkTagDrawer selectedIds={["1"]} onChange={change} onClose={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
    target: { value: "env" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Tag value" }), {
    target: { value: "prod" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  const commit = await screen.findByRole("button", {
    name: "Confirm tag change",
  });
  expect((commit as HTMLButtonElement).disabled).toBe(true);
  expect(document.activeElement).toBe(
    screen.getByText("0 monitors will change; 1 unchanged."),
  );
  view.rerender(
    <BulkTagDrawer
      selectedIds={Array.from({ length: 10001 }, (_, i) => String(i + 1))}
      onChange={change}
      onClose={vi.fn()}
    />,
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Preview change",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText(/Select at most 10000 monitors/)).toBeTruthy();
});

it.each(["remove", "rename_value"] as const)(
  "expresses %s scope and sends only the relevant fields",
  async (action) => {
    const change = vi.fn<TagChange>(async () => result);
    render(
      <BulkTagDrawer
        selectedIds={["1", "2"]}
        onChange={change}
        onClose={vi.fn()}
      />,
    );
    fireEvent.change(screen.getByRole("combobox", { name: "Action" }), {
      target: { value: action },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
      target: { value: "env" },
    });
    fireEvent.change(screen.getByRole("textbox", { name: "Tag value" }), {
      target: { value: "prod" },
    });
    if (action === "rename_value")
      fireEvent.change(screen.getByRole("textbox", { name: "New value" }), {
        target: { value: "Production" },
      });
    fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
    await screen.findByRole("button", { name: "Confirm tag change" });
    expect(change.mock.calls[0]).toEqual([
      action === "remove"
        ? { action, monitor_ids: [1, 2], key: "env", value: "prod" }
        : { action, key: "env", value: "prod", new_value: "Production" },
      undefined,
    ]);
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Confirm tag change" }),
    );
    expect(
      screen.getByText(/Only the exact key\/value pair changes/),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Confirm tag change" }));
    await waitFor(() => expect(change).toHaveBeenCalledTimes(2));
    expect(change.mock.calls[1][1]).toBe(etag);
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByText("Changed 1 monitors; 1 unchanged."),
      ),
    );
  },
);
