// @vitest-environment jsdom
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AddMonitor } from "./AddMonitor";
import { AddMonitorForm } from "./AddMonitorForm";
import {
  ApiError,
  describePreview,
  previewCheck,
  suggestName,
} from "./preview";
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
    expect(suggestName("https://example.com/health?full=1")).toBe(
      "example.com",
    );
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
      result({
        ok: false,
        error: "connection refused",
        target: "https://down.example.com",
      }),
    );
    expect(text).toContain("connection refused");
    // The target may not be what was typed; repeating it is the point.
    expect(text).toContain("https://down.example.com");
  });
});

describe("AddMonitorForm", () => {
  it("needs only a target: the name follows the address until it is edited", () => {
    const onSubmit = vi.fn();
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={onSubmit}
        preview={{ phase: "idle" }}
      />,
    );

    setField(/what should be watched/i, "example.com");
    expect(field(/^name$/i).value).toBe("example.com");

    click(/save monitor/i);
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ name: "example.com" }),
    );
  });

  it("stops following the address once the name has been typed in", () => {
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
      />,
    );

    setField(/what should be watched/i, "example.com");
    setField(/^name$/i, "Marketing site");
    setField(/what should be watched/i, "example.com/health");

    expect(field(/^name$/i).value).toBe("Marketing site");
  });

  it("will not test or save an empty target", () => {
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
      />,
    );
    expect(
      screen.getByRole("button", { name: /test it/i }).hasAttribute("disabled"),
    ).toBe(true);
    expect(
      screen
        .getByRole("button", { name: /save monitor/i })
        .hasAttribute("disabled"),
    ).toBe(true);
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
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={onSubmit}
        preview={{ phase: "idle" }}
      />,
    );

    setField(/what should be watched/i, "example.com");
    setField(/body must contain/i, "welcome");
    click(/save monitor/i);

    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({
        keyword: "welcome",
        keywordMode: "must_contain",
      }),
    );
  });
});

describe("AddMonitor", () => {
  it("previews without saving, then saves what the preview resolved", async () => {
    // A TCP target on purpose: `http` is also the fallback when no preview
    // has run, so an http fixture would pass even if the echo were ignored.
    const preview = vi
      .fn()
      .mockResolvedValue(
        result({
          type: "tcp",
          target: "db.example.com:5432",
          status_code: undefined,
        }),
      );
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
    const preview = vi
      .fn()
      .mockRejectedValue(new ApiError(400, "could not tell what to check"));

    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    setField(/what should be watched/i, "??");
    click(/test it/i);

    await waitFor(() =>
      expect(statusText()).toMatch(/could not tell what to check/i),
    );
  });

  it("tells the rate limit apart from a failure, and says how long to wait", async () => {
    const preview = vi
      .fn()
      .mockRejectedValue(
        new ApiError(429, "a preview check ran moments ago", 3),
      );
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
    const preview = vi
      .fn()
      .mockResolvedValue(
        result({ type: "http", target: "https://example.com" }),
      );
    const create = vi.fn().mockResolvedValue({ id: "1" });
    render(<AddMonitor api={{ preview, create }} />);

    setField(/what should be watched/i, "example.com");
    click(/test it/i);
    await waitFor(() => expect(statusText()).toMatch(/example\.com/));

    // Changing the timeout changes what the probe proved, so the result no
    // longer describes the form.
    fireEvent.change(screen.getByLabelText(/give up after/i), {
      target: { value: "45" },
    });
    click(/save monitor/i);

    await waitFor(() =>
      expect(screen.getByRole("alert").textContent ?? "").toMatch(/test it/i),
    );
    expect(create).not.toHaveBeenCalled();
  });

  it("saves the type and target the preview actually resolved", async () => {
    const preview = vi
      .fn()
      .mockResolvedValue(
        result({ type: "http", target: "https://example.com" }),
      );
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
    const create = vi
      .fn()
      .mockRejectedValue(new ApiError(400, "name is required"));
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);

    setField(/what should be watched/i, "example.com");
    // An explicit type, so the save actually reaches the server: an untested
    // bare address is refused locally now and would never call create().
    fireEvent.change(screen.getByLabelText(/check type/i), {
      target: { value: "http" },
    });
    click(/save monitor/i);

    await waitFor(() =>
      expect(screen.getByRole("alert").textContent ?? "").toMatch(
        /name is required/i,
      ),
    );
  });
});

describe("a rejection that names a field", () => {
  /*
   * The reason this is tested through the API boundary rather than by poking
   * the form's props: the value has to survive three hops — the JSON body,
   * ApiError, and the form's placement rule — and the failure mode SUB-70 is
   * about is a silent one. If `field` is dropped anywhere along the way the
   * message still renders, just in the global region, so only an assertion
   * about *where* it landed catches it.
   */

  it("marks the named input invalid and describes it with the message", async () => {
    const preview = vi
      .fn()
      .mockRejectedValue(
        new ApiError(
          400,
          "an http monitor needs a target starting with http://",
          null,
          "target",
        ),
      );

    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    setField(/what should be watched/i, "ftp://example.com");
    click(/test it/i);

    const target = field(/what should be watched/i);
    await waitFor(() =>
      expect(target.getAttribute("aria-invalid")).toBe("true"),
    );

    // The message must be reachable from the input, not merely present on the
    // page: aria-invalid says "wrong" without saying why.
    const describedBy = target.getAttribute("aria-describedby") ?? "";
    const ids = describedBy.split(" ").filter((id) => id !== "");
    const described = ids
      .map((id) => document.getElementById(id)?.textContent ?? "")
      .join(" ");
    expect(described).toMatch(/needs a target starting with/i);

    // And it must not also be shouting from the global region; the same
    // sentence twice reads as two separate problems.
    expect(statusText()).not.toMatch(/needs a target starting with/i);
  });

  /*
   * The advanced panel and a rejection it would hide.
   *
   * Driven through AddMonitorForm with a `saveError` prop rather than through
   * AddMonitor: the container refuses to call `create` at all until a bare
   * address has been previewed, so a server rejection about interval_s is not
   * reachable from the outside without also staging a preview. The panel logic
   * lives in the form, and this is the input it actually reacts to.
   */
  const panel = () =>
    document.querySelector("details.add-advanced") as HTMLDetailsElement;

  it("opens the advanced panel when the rejection is about a control inside it", () => {
    // interval_s lives in the collapsed <details>. Its message was rendered
    // under the input, and both global notices are suppressed once a message
    // has been placed — so the panel hid the only copy and the form said
    // nothing at all about a save that failed.
    const { rerender } = render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
      />,
    );
    expect(
      panel().open,
      "the panel starts closed, or this proves nothing",
    ).toBe(false);

    rerender(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
        saveError={{
          field: "interval_s",
          message: "interval_s must be between 20 and 86400",
        }}
      />,
    );

    expect(panel().open).toBe(true);
    const described = (
      field(/check every/i).getAttribute("aria-describedby") ?? ""
    )
      .split(" ")
      .filter((id) => id !== "")
      .map((id) => document.getElementById(id)?.textContent ?? "")
      .join(" ");
    expect(described).toMatch(/between 20 and 86400/i);
  });

  it("leaves the panel closed when the rejection is about a visible field", () => {
    // Opening it for a target error would push the buttons down the page and
    // answer a question nobody asked, while the message it reveals was already
    // on screen.
    render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
        saveError={{ field: "target", message: "target has no host" }}
      />,
    );

    expect(field(/what should be watched/i).getAttribute("aria-invalid")).toBe(
      "true",
    );
    expect(panel().open).toBe(false);
  });

  it("reopens the panel for a second hidden rejection after the user closed it", () => {
    // Two things at once, because they are the same bug from either side.
    //
    // Closing has to stick: `open` is driven from state, so if the user's own
    // toggle is not written back the component believes the panel is open
    // while it is not — and the next hidden rejection then reveals nothing,
    // because "open it" is already what the state says.
    //
    // And a second, different complaint about a hidden control has to be seen,
    // even though the user closed the panel on the first one. Two answers are
    // two answers.
    const rejectedWith = (message: string) => (
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
        saveError={{ field: "timeout_s", message }}
      />
    );
    const { rerender } = render(
      <AddMonitorForm
        onPreview={noop}
        onSubmit={noop}
        preview={{ phase: "idle" }}
      />,
    );

    rerender(rejectedWith("timeout_s must be between 1 and 120"));
    expect(panel().open).toBe(true);

    // jsdom does not implement the <summary> click that toggles a <details>,
    // so the close is delivered the way a browser reports it.
    panel().open = false;
    fireEvent(panel(), new Event("toggle"));
    expect(panel().open).toBe(false);

    rerender(rejectedWith("timeout_s must be a whole number of seconds"));
    expect(panel().open).toBe(true);
  });

  it("leaves a rate limit in the global region and blames no input", async () => {
    const preview = vi
      .fn()
      .mockRejectedValue(
        new ApiError(429, "a preview check ran moments ago", 3),
      );

    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    setField(/what should be watched/i, "example.com");
    click(/test it/i);

    await waitFor(() => expect(statusText()).toMatch(/about 3s/i));
    // Going too fast is not a complaint about the address, and marking the
    // target invalid would send the user to edit something that is correct.
    expect(
      field(/what should be watched/i).getAttribute("aria-invalid"),
    ).toBeNull();
  });

  it("shows a field it cannot place in the global region rather than dropping it", async () => {
    // ssl_warn_days has no control on this form. The message still has to be
    // readable somewhere — silently swallowing it is the worst outcome.
    const preview = vi
      .fn()
      .mockRejectedValue(
        new ApiError(
          400,
          "ssl_warn_days must be between 1 and 365",
          null,
          "ssl_warn_days",
        ),
      );

    render(<AddMonitor api={{ preview, create: vi.fn() }} />);
    setField(/what should be watched/i, "example.com");
    click(/test it/i);

    await waitFor(() =>
      expect(statusText()).toMatch(/ssl_warn_days must be between/i),
    );
  });

  it("puts a save rejection under the field the server blamed", async () => {
    const create = vi
      .fn()
      .mockRejectedValue(
        new ApiError(400, "name cannot be empty", null, "name"),
      );

    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    setField(/what should be watched/i, "example.com");
    setField(/^name$/i, "x");
    fireEvent.change(screen.getByLabelText(/check type/i), {
      target: { value: "http" },
    });
    click(/save monitor/i);

    await waitFor(() =>
      expect(field(/^name$/i).getAttribute("aria-invalid")).toBe("true"),
    );
  });
});

describe("reading an error body", () => {
  /*
   * The hop the rest of this file cannot cover. Every test above builds an
   * ApiError by hand, so none of them would notice if the `field` key were
   * dropped while parsing the response — the message would still arrive, and
   * only its placement would change. This asserts the parse itself.
   */
  function respond(
    body: unknown,
    status = 400,
    headers: Record<string, string> = {},
  ) {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify(body), {
          status,
          headers: { "Content-Type": "application/json", ...headers },
        }),
      ),
    );
  }

  afterEach(() => vi.unstubAllGlobals());

  it("carries the field the server named", async () => {
    respond({ error: "target has no host", field: "target" });
    const err = await previewCheck({ target: "https://" }).catch(
      (e: unknown) => e,
    );
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).field).toBe("target");
    expect((err as ApiError).message).toBe("target has no host");
  });

  it("treats a missing field as no field rather than as a blank name", async () => {
    respond({ error: "invalid JSON" });
    const err = await previewCheck({ target: "x" }).catch((e: unknown) => e);
    expect((err as ApiError).field).toBeNull();
  });

  it("treats an empty field as absent", async () => {
    // The server omits the key, but a proxy or an older build might send "".
    // A field named "" matches no input, so it must not be placed.
    respond({ error: "something went wrong", field: "" });
    const err = await previewCheck({ target: "x" }).catch((e: unknown) => e);
    expect((err as ApiError).field).toBeNull();
  });
});
