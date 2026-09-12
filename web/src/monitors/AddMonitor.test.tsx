// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AddMonitor } from "./AddMonitor";
import { AddMonitorForm } from "./AddMonitorForm";
import { ApiError, describePreview, suggestName } from "./preview";
import type { PreviewResult } from "./preview";

/*
 * Plain DOM assertions, not jest-dom: this suite does not install the matcher
 * package, and adding a dependency to spell `toHaveTextContent` would be a new
 * install for every contributor in exchange for `.textContent`.
 */

function result(over: Partial<PreviewResult> = {}): PreviewResult {
  return {
    ok: true,
    checked_at: "2026-01-01T12:00:00Z",
    latency_ms: 120,
    status_code: 200,
    type: "http",
    target: "https://example.com",
    ...over,
  };
}

const noop = () => {};

afterEach(cleanup);

/** Replaces a controlled field's value in one go, as a paste would. */
function setField(label: RegExp, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function field(label: RegExp): HTMLInputElement {
  return screen.getByLabelText(label) as HTMLInputElement;
}

function click(name: RegExp) {
  fireEvent.click(screen.getByRole("button", { name }));
}

function statusText(): string {
  return screen.getByRole("status").textContent ?? "";
}

describe("suggestName", () => {
  it("takes the host, so the name field costs nothing to fill", () => {
    expect(suggestName("https://example.com/health?full=1")).toBe("example.com");
    expect(suggestName("example.com")).toBe("example.com");
    expect(suggestName("db.example.com:5432")).toBe("db.example.com");
  });

  it("is empty for an empty target rather than inventing something", () => {
    expect(suggestName("   ")).toBe("");
  });
});

describe("describePreview", () => {
  it("answers the question that was asked, not the status code", () => {
    expect(describePreview(result())).toContain("answered");
    expect(describePreview(result())).toContain("120 ms");
  });

  it("quotes the server's reason on failure, and names the target probed", () => {
    const text = describePreview(
      result({ ok: false, error: "connection refused", target: "https://down.example.com" }),
    );
    expect(text).toContain("connection refused");
    // The target may not be what was typed; repeating it is the point.
    expect(text).toContain("https://down.example.com");
  });
});

describe("AddMonitorForm", () => {
  it("needs only a target: the name follows the address until it is edited", () => {
    const onSubmit = vi.fn();
    render(<AddMonitorForm onPreview={noop} onSubmit={onSubmit} preview={{ phase: "idle" }} />);

    setField(/what should be watched/i, "example.com");
    expect(field(/^name$/i).value).toBe("example.com");

    click(/save monitor/i);
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ name: "example.com" }));
  });

  it("stops following the address once the name has been typed in", () => {
    render(<AddMonitorForm onPreview={noop} onSubmit={noop} preview={{ phase: "idle" }} />);

    setField(/what should be watched/i, "example.com");
    setField(/^name$/i, "Marketing site");
    setField(/what should be watched/i, "example.com/health");

    expect(field(/^name$/i).value).toBe("Marketing site");
  });

  it("will not test or save an empty target", () => {
    render(<AddMonitorForm onPreview={noop} onSubmit={noop} preview={{ phase: "idle" }} />);
    expect(screen.getByRole("button", { name: /test it/i }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("button", { name: /save monitor/i }).hasAttribute("disabled")).toBe(
      true,
    );
  });

  it("says what the check was resolved to, since the form stopped asking", () => {
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{
          phase: "done",
          result: result({ type: "tcp", target: "db.example.com:5432" }),
          // The form renders a result without inspecting the fingerprint; only
          // the save path compares it.
          request: "",
        }}
      />,
    );
    expect(statusText()).toMatch(/TCP connection/i);
  });

  it("keeps a rejected target distinct from a failed check", () => {
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "rejected", message: "could not tell what to check" }}
      />,
    );
    expect(statusText()).toMatch(/could not tell what to check/i);
    // Nothing was contacted, so it must not read as a down target.
    expect(statusText()).not.toMatch(/did not answer/i);
  });

  it("turns on keyword matching when a keyword is typed", () => {
    const onSubmit = vi.fn();
    render(<AddMonitorForm onPreview={noop} onSubmit={onSubmit} preview={{ phase: "idle" }} />);

    setField(/what should be watched/i, "example.com");
    setField(/body must contain/i, "welcome");
    click(/save monitor/i);

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ keyword: "welcome", keywordMode: "must_contain" }),
    );
  });
});

describe("AddMonitor", () => {
  it("previews without saving, then saves what the preview resolved", async () => {
    // A TCP target on purpose: `http` is also the fallback when no preview
    // has run, so an http fixture would pass even if the echo were ignored.
    const preview = vi
      .fn()
      .mockResolvedValue(result({ type: "tcp", target: "db.example.com:5432", status_code: undefined }));
    const create = vi.fn().mockResolvedValue({ id: "7" });
    const onCreated = vi.fn();

    render(<AddMonitor api={{ preview, create }} onCreated={onCreated} />);

    setField(/what should be watched/i, "db.example.com:5432");
    click(/test it/i);

    await waitFor(() => expect(statusText()).toMatch(/answered/i));
    expect(preview).toHaveBeenCalledWith(
      expect.objectContaining({ target: "db.example.com:5432" }),
      expect.anything(),
    );
    // A preview must not create anything. That is the whole contract.
    expect(create).not.toHaveBeenCalled();

    click(/save monitor/i);
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("7"));
    // The server inferred https and http; saving must reuse that rather than
    // repeat the inference here.
    expect(create).toHaveBeenCalledWith(
      expect.objectContaining({ type: "tcp", target: "db.example.com:5432" }),
    );
  });

  it("reports a rejected target as the server worded it", async () => {
    const preview = vi.fn().mockRejectedValue(new ApiError(400, 'could not tell what to check'));

    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    setField(/what should be watched/i, "??");
    click(/test it/i);

    await waitFor(() => expect(statusText()).toMatch(/could not tell what to check/i));
  });

  it("tells the rate limit apart from a failure, and says how long to wait", async () => {
    const preview = vi
      .fn()
      .mockRejectedValue(new ApiError(429, "a preview check ran moments ago", 3));
    render(<AddMonitor api={{ preview, create: vi.fn() }} />);

    setField(/what should be watched/i, "example.com");
    click(/test it/i);

    await waitFor(() => expect(statusText()).toMatch(/about 3s/i));
  });

  it("does not let a slow first answer overwrite a newer one", async () => {
    let releaseFirst: (value: PreviewResult) => void = () => {};
    const preview = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<PreviewResult>((resolve) => {
            releaseFirst = resolve;
          }),
      )
      .mockResolvedValueOnce(result({ target: "https://second.example.com" }));

    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    setField(/what should be watched/i, "first.example.com");
    click(/test it/i);
    setField(/what should be watched/i, "second.example.com");
    // The button stays pressable while a probe is running: someone who has
    // just spotted their typo should not have to wait out a ten-second
    // timeout before they may correct it.
    click(/testing/i);

    await waitFor(() => expect(statusText()).toMatch(/second\.example\.com/));

    // The stale answer lands late and must be ignored.
    releaseFirst(result({ target: "https://first.example.com" }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(statusText()).not.toMatch(/first\.example\.com/);
  });

  it("will not save an inferred type against a preview of different settings", async () => {
    // The bug this closes: preview `example.com`, which the server infers as
    // https, then switch the dropdown to Ping and save. The stale result was
    // still considered valid because only the target was compared, so a ping
    // monitor was created for a target that had only ever been tested as HTTP.
    const preview = vi.fn().mockResolvedValue(result({ type: "http", target: "https://example.com" }));
    const create = vi.fn().mockResolvedValue({ id: "1" });
    render(<AddMonitor api={{ preview, create }} />);

    setField(/what should be watched/i, "example.com");
    click(/test it/i);
    await waitFor(() => expect(statusText()).toMatch(/example\.com/));

    // Changing the timeout changes what the probe proved, so the result no
    // longer describes the form.
    fireEvent.change(screen.getByLabelText(/give up after/i), { target: { value: "45" } });
    click(/save monitor/i);

    await waitFor(() => expect(screen.getByRole("alert").textContent ?? "").toMatch(/test it/i));
    expect(create).not.toHaveBeenCalled();
  });

  it("saves the type and target the preview actually resolved", async () => {
    const preview = vi.fn().mockResolvedValue(result({ type: "http", target: "https://example.com" }));
    const create = vi.fn().mockResolvedValue({ id: "1" });
    render(<AddMonitor api={{ preview, create }} />);

    setField(/what should be watched/i, "example.com");
    click(/test it/i);
    await waitFor(() => expect(statusText()).toMatch(/example\.com/));

    click(/save monitor/i);
    await waitFor(() => expect(create).toHaveBeenCalled());
    const sent = create.mock.calls[0][0] as { type: string; target: string };
    // The normalised target, not the raw text: the monitor that gets created
    // has to be the one that was tested.
    expect(sent.type).toBe("http");
    expect(sent.target).toBe("https://example.com");
  });

  it("surfaces a save failure as an alert, not as a preview result", async () => {
    const create = vi.fn().mockRejectedValue(new ApiError(400, "name is required"));
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);

    setField(/what should be watched/i, "example.com");
    // An explicit type, so the save actually reaches the server: an untested
    // bare address is refused locally now and would never call create().
    fireEvent.change(screen.getByLabelText(/check type/i), { target: { value: "http" } });
    click(/save monitor/i);

    await waitFor(() =>
      expect(screen.getByRole("alert").textContent ?? "").toMatch(/name is required/i),
    );
  });
});
