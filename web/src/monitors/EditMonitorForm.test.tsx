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
    // Silence would read as a form that forgot them. The reason is that this
    // screen never received their current values, and a blank input invites
    // erasing a setting the user cannot see.
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    expect(
      screen.getByText(/never received their current values/),
    ).toBeTruthy();
  });

  it("warns that saving tags replaces the whole set", () => {
    render(<EditMonitorForm monitor={make()} onSave={vi.fn()} />);
    expect(screen.getByText(/a tag left out here is a tag removed/i)).toBeTruthy();
  });
});
