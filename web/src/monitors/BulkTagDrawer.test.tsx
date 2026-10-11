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
const none = { routing_rules: 0, maintenance_windows: 0, status_pages: 0 };
const result = { total: 2, changed: 1, unchanged: 1, collisions: 0, ...none, etag };
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
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("alert")));
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
    ...none,
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
  await waitFor(() => expect(document.activeElement).toBe(
    screen.getByText("0 monitors will change; 1 unchanged."),
  ));
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
    // DOM presence and React's post-commit focus effect are different states.
    await waitFor(() => expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Confirm tag change" }),
    ));
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

it("says which rules, windows and pages a rename moves before the confirm", async () => {
  const moved = {
    ...result,
    routing_rules: 1,
    maintenance_windows: 0,
    status_pages: 2,
  };
  const change = vi.fn<TagChange>(async () => moved);
  render(
    <BulkTagDrawer selectedIds={[]} onChange={change} onClose={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Action" }), {
    target: { value: "rename_value" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
    target: { value: "env" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Tag value" }), {
    target: { value: "prod" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "New value" }), {
    target: { value: "production" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  await screen.findByRole("button", { name: "Confirm tag change" });
  expect(
    screen.getByText("Also moves to the new tag: 1 routing rule, 2 status pages."),
  ).toBeTruthy();
  // The confirm writes exactly what the preview said (same validator), so
  // the result does not repeat the list.
  fireEvent.click(screen.getByRole("button", { name: "Confirm tag change" }));
  await waitFor(() =>
    expect(document.activeElement?.textContent).toBe(
      "Changed 1 monitors; 1 unchanged.",
    ),
  );
});

it("lets a rename that moves only configuration be confirmed", async () => {
  // No monitor carries the old pair any more, but a page still names it:
  // the rename is how that page gets pointed at the new tag.
  const change = vi.fn<TagChange>(async () => ({
    ...result,
    total: 0,
    changed: 0,
    unchanged: 0,
    status_pages: 1,
  }));
  render(
    <BulkTagDrawer selectedIds={[]} onChange={change} onClose={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("combobox", { name: "Action" }), {
    target: { value: "rename_key" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
    target: { value: "env" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "New key" }), {
    target: { value: "stage" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  const commit = await screen.findByRole("button", {
    name: "Confirm tag change",
  });
  expect(screen.getByText("Also moves to the new tag: 1 status page.")).toBeTruthy();
  expect((commit as HTMLButtonElement).disabled).toBe(false);
  await waitFor(() => expect(document.activeElement).toBe(commit));
});

it("says nothing about followers for a selection-scoped change", async () => {
  // Apply and remove never move configuration; a server bug reporting
  // otherwise must not be repeated to the operator as a promise.
  const change = vi.fn<TagChange>(async () => ({ ...result, routing_rules: 1 }));
  render(
    <BulkTagDrawer selectedIds={["1"]} onChange={change} onClose={vi.fn()} />,
  );
  fireEvent.change(screen.getByRole("textbox", { name: "Tag key" }), {
    target: { value: "env" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "Tag value" }), {
    target: { value: "prod" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview change" }));
  await screen.findByRole("button", { name: "Confirm tag change" });
  expect(screen.queryByText(/Also moves/)).toBeNull();
});
