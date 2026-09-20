// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { EditMonitorForm } from "./EditMonitorForm";
import { inventoryFromApi } from "./inventory";

afterEach(cleanup);

function make(over: Record<string, unknown> = {}) {
  return inventoryFromApi({
    id: 1,
    name: "auth",
    type: "http",
    target: "https://auth.example.com",
    interval_s: 60,
    timeout_s: 10,
    enabled: true,
    status: "up",
    created_at: "2026-09-01T10:00:00Z",
    tags: { env: "prod" },
    ...over,
  } as Parameters<typeof inventoryFromApi>[0]);
}

describe("EditMonitorForm", () => {
  it("sends only the fields that changed", async () => {
    /*
     * The whole point of PATCH is that "not sent" and "sent as zero" stay
     * distinguishable. Sending every field on every save throws that away on
     * the client side: two people editing different fields of one monitor
     * would each overwrite the other with a value they never touched.
     */
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make()} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "auth-eu" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({ name: "auth-eu" });
  });

  it("refuses to send anything when nothing changed", () => {
    const onSave = vi.fn();
    render(<EditMonitorForm monitor={make()} onSave={onSave} />);
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toMatch(/nothing changed/i);
  });

  it("rejects an out-of-range interval before the server has to", () => {
    const onSave = vi.fn();
    render(<EditMonitorForm monitor={make()} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText(/interval/i), {
      target: { value: "5" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toMatch(/between 20 and 86400/);
  });

  it("refuses tag text it cannot parse, rather than sending a guess", () => {
    const onSave = vi.fn();
    render(<EditMonitorForm monitor={make()} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Tags"), {
      target: { value: "prod" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toMatch(/key:value/);
    expect(onSave).not.toHaveBeenCalled();
  });

  it("shows the server's own sentence when the save is refused", async () => {
    // A 412 says someone else edited this monitor. Swallowing it would leave
    // the user believing an edit landed that did not.
    const onSave = vi
      .fn()
      .mockRejectedValue(new Error("monitor 1 was modified by someone else"));
    render(<EditMonitorForm monitor={make()} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "auth-eu" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toMatch(
        /modified by someone else/,
      ),
    );
  });

  it("offers no timeout control for a push monitor, which has none", () => {
    // Absent rather than disabled: a disabled input reads as a setting that is
    // temporarily unavailable, and this one does not exist.
    render(
      <EditMonitorForm
        monitor={make({ type: "push", target: "", push_interval_s: 3600 })}
        onSave={vi.fn()}
      />,
    );
    expect(screen.queryByLabelText(/timeout/i)).toBeNull();
  });

  it("says why target, type and the HTTP settings are not editable here", () => {
    // The detail endpoint carries these settings, but this form has no
    // controls for them yet. Saving must leave them unchanged.
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    expect(
      screen.getByText(/headers are not yet editable here/),
    ).toBeTruthy();
  });

  it("lets one tag be edited on a monitor whose other tag holds a comma", async () => {
    /*
     * The residual half of the round-trip bug. Remembering the initial text
     * covers a save that never touched the tags; this covers the one that did.
     * With a comma separator, changing `env` here would have made the
     * untouched `note:a,b` read as a bare `b` and refused the save.
     */
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <EditMonitorForm
        monitor={make({ tags: { note: "a,b", env: "prod" } })}
        onSave={onSave}
      />,
    );
    const field = screen.getByLabelText("Tags") as HTMLTextAreaElement;
    fireEvent.change(field, {
      target: { value: field.value.replace("env:prod", "env:staging") },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({
      tags: { note: "a,b", env: "staging" },
    });
  });

  it("does not send tags at all for an edit that never touched them", async () => {
    /*
     * PATCH replaces the whole tag set, so a patch that carries `tags` on
     * every save turns any drift between what is shown and what is stored
     * into a silent data change on an unrelated edit. The line format makes
     * the round trip exact, so the re-parsed text equals what was stored and
     * the field is correctly left out of the patch.
     */
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <EditMonitorForm monitor={make({ tags: { env: "prod" } })} onSave={onSave} />,
    );
    fireEvent.change(screen.getByLabelText(/interval/i), {
      target: { value: "120" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({ interval_s: 120 });
    expect(onSave.mock.calls[0][0]).not.toHaveProperty("tags");
  });

  it("saves a rename when a stored tag value contains a comma", async () => {
    /*
     * `note: "a,b"` is a legal stored tag — the API carries an object so a
     * value may contain punctuation — but rendered as `note:a,b` it reads back
     * as two entries, the second with no key. Re-parsing that on every save
     * refused every save, including a rename that never touched the tags.
     */
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <EditMonitorForm monitor={make({ tags: { note: "a,b" } })} onSave={onSave} />,
    );
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "auth-eu" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    // And the tags are not sent at all, so nothing can be mangled on the way.
    expect(onSave.mock.calls[0][0]).toEqual({ name: "auth-eu" });
  });

  it("puts a field rejection beside the field and moves focus there", () => {
    /*
     * A message under the submit button is one a screen reader user has to go
     * looking for, and one a sighted user reads after the field they then have
     * to scroll back up to fix.
     */
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    const interval = screen.getByLabelText(/interval/i);
    fireEvent.change(interval, { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));

    expect(interval.getAttribute("aria-invalid")).toBe("true");
    const describedBy = interval.getAttribute("aria-describedby");
    expect(describedBy).toBeTruthy();
    expect(document.getElementById(describedBy!)?.textContent).toMatch(
      /between 20 and 86400/,
    );
    expect(document.activeElement).toBe(interval);
  });

  it("leaves a rejection that is about no single field unpinned", () => {
    // "Nothing changed" is not about an input, and pinning it under one would
    // tell the user to edit something that is not wrong.
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(screen.getByLabelText("Name").getAttribute("aria-invalid")).toBeNull();
    expect(screen.getByRole("alert").textContent).toMatch(/nothing changed/i);
  });

  it("does not send a TLS floor when the field was never touched", async () => {
    /*
     * The failure this closes: a monitor with no floor, opened to be renamed,
     * saved with min_tls_version: "1.2" because the select defaulted to the
     * current default. That silently pins today's floor onto a monitor whose
     * owner asked for nothing, and the nullable column exists to keep those
     * two states apart.
     */
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<EditMonitorForm monitor={make()} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "auth-eu" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).not.toHaveProperty("min_tls_version");
  });

  it("loads the stored floor rather than a default, and sends a change", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <EditMonitorForm
        monitor={make({ min_tls_version: "1.0" })}
        onSave={onSave}
      />,
    );
    const select = screen.getByLabelText(
      /minimum tls version/i,
    ) as HTMLSelectElement;
    expect(select.value).toBe("1.0");
    fireEvent.change(select, { target: { value: "1.3" } });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({ min_tls_version: "1.3" });
  });

  it("sends an empty string to take an existing floor back off", async () => {
    // The only way to undo a floor from this form. PATCH reads "" as "clear
    // it"; omitting the field would leave the old floor in place.
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(
      <EditMonitorForm
        monitor={make({ min_tls_version: "1.3" })}
        onSave={onSave}
      />,
    );
    fireEvent.change(screen.getByLabelText(/minimum tls version/i), {
      target: { value: "" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({ min_tls_version: "" });
  });

  it("explains both directions of the TLS floor, not just the numbers", () => {
    /*
     * The control without the copy is four numbers that read as a client
     * setting. Both cases have to be on screen: that raising it is an
     * assertion whose failure is the point, and that lowering it is what
     * makes an old appliance checkable at all.
     */
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    const help =
      screen
        .getByLabelText(/minimum tls version/i)
        .getAttribute("aria-describedby") ?? "";
    const text = document.getElementById(help.split(" ")[0])?.textContent ?? "";
    expect(text).toMatch(/assertion/i);
    expect(text).toMatch(/red/i);
    expect(text).toMatch(/1\.0/);
    expect(text).toMatch(/refused handshake/i);
    expect(text).toMatch(/floor SubGlance dials with/i);
  });

  it("warns that saving tags replaces the whole set", () => {
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    expect(screen.getByText(/a tag left out here is a tag removed/i)).toBeTruthy();
  });
});
