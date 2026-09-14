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
import { MonitorDetail } from "./MonitorDetail";
import { PushUrlReveal } from "./PushUrlReveal";
import { curlLine, curlStatusLine } from "./push";
import { describeTarget } from "./format";
import type { Monitor } from "./types";

afterEach(cleanup);

const URL_ = "http://box.local:8080/api/v1/push/sgu_secrettoken";

function setField(label: RegExp, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

function click(name: RegExp) {
  fireEvent.click(screen.getByRole("button", { name }));
}

/** Picks the push type, which is what switches the form into push mode. */
function choosePush() {
  fireEvent.change(screen.getByLabelText(/check type/i), {
    target: { value: "push" },
  });
}

const pushMonitor = (over: Partial<Monitor> = {}): Monitor => ({
  id: "9",
  name: "Nightly backup",
  status: "waiting",
  target: "",
  latencyMs: null,
  uptime24h: null,
  beats: [],
  lastCheck: null,
  tags: {},
  push: { intervalS: 3600, graceS: 300, tokenPrefix: "sgu_abcd" },
  ...over,
});

describe("the add form in push mode", () => {
  it("stops asking for a target, because the API rejects one", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9", pushUrl: URL_ });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);

    expect(screen.queryByLabelText(/what should be watched/i)).not.toBeNull();
    choosePush();
    expect(screen.queryByLabelText(/what should be watched/i)).toBeNull();

    setField(/^name$/i, "Nightly backup");
    click(/save monitor/i);

    await waitFor(() => expect(create).toHaveBeenCalled());
    const body = create.mock.calls[0][0] as Record<string, unknown>;
    expect(body.type).toBe("push");
    expect(body).not.toHaveProperty("target");
    expect(body.push_interval_s).toBe(3600);
    expect(body.push_grace_s).toBe(60);
  });

  it("offers the window as plain fields rather than behind the disclosure", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    choosePush();
    // Required fields, so they must be reachable without opening anything.
    const interval = screen.getByLabelText(
      /should report every/i,
    ) as HTMLInputElement;
    expect(interval.closest("details")).toBeNull();
    expect(
      screen.getByLabelText(/allow it to be late by/i).closest("details"),
    ).toBeNull();
  });

  it("hides Test it, which would probe a target that does not exist", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    expect(screen.queryByRole("button", { name: /test it/i })).not.toBeNull();
    choosePush();
    expect(screen.queryByRole("button", { name: /test it/i })).toBeNull();
  });

  it("sends the window the user typed, not the default", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9", pushUrl: URL_ });
    render(<AddMonitor api={{ preview: vi.fn(), create }} />);
    choosePush();
    setField(/^name$/i, "Nightly backup");
    setField(/should report every/i, "86400");
    setField(/allow it to be late by/i, "1800");
    click(/save monitor/i);

    await waitFor(() => expect(create).toHaveBeenCalled());
    expect(create.mock.calls[0][0]).toMatchObject({
      push_interval_s: 86400,
      push_grace_s: 1800,
    });
  });

  it("requires a name, since there is no address to fall back on", () => {
    render(<AddMonitor api={{ preview: vi.fn(), create: vi.fn() }} />);
    choosePush();
    const save = screen.getByRole("button", {
      name: /save monitor/i,
    }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    setField(/^name$/i, "Nightly backup");
    expect(save.disabled).toBe(false);
  });
});

describe("the one-time push URL", () => {
  it("is shown after creating, instead of closing over it", async () => {
    const create = vi.fn().mockResolvedValue({ id: "9", pushUrl: URL_ });
    const onCreated = vi.fn();
    render(
      <AddMonitor api={{ preview: vi.fn(), create }} onCreated={onCreated} />,
    );
    choosePush();
    setField(/^name$/i, "Nightly backup");
    click(/save monitor/i);

    await waitFor(() =>
      expect(screen.queryByLabelText(/push url/i)).not.toBeNull(),
    );
    // The dialog must stay open: the caller closes it on onCreated, and the
    // URL cannot be retrieved a second time.
    expect(onCreated).not.toHaveBeenCalled();
    expect((screen.getByLabelText(/push url/i) as HTMLInputElement).value).toBe(
      URL_,
    );

    click(/i have saved it/i);
    expect(onCreated).toHaveBeenCalledWith("9");
  });

  it("closes straight away for a monitor with no URL to lose", async () => {
    const create = vi.fn().mockResolvedValue({ id: "4" });
    const onCreated = vi.fn();
    render(
      <AddMonitor api={{ preview: vi.fn(), create }} onCreated={onCreated} />,
    );
    setField(/what should be watched/i, "example.com");
    fireEvent.change(screen.getByLabelText(/check type/i), {
      target: { value: "http" },
    });
    click(/save monitor/i);
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith("4"));
  });

  it("says plainly that it cannot be recovered", () => {
    render(<PushUrlReveal url={URL_} name="Nightly backup" />);
    expect(screen.getByRole("alert").textContent ?? "").toMatch(/shown once/i);
  });

  it("takes focus, because the button that opened it has unmounted", () => {
    // The submit button is gone by the time this renders, so without an
    // explicit move focus sits on <body> and the next Tab restarts at the top
    // of the page — past every Copy button holding the only copy of the URL.
    render(<PushUrlReveal url={URL_} name="Nightly backup" />);
    expect(document.activeElement).toBe(
      screen.getByRole("region", { name: /nightly backup/i }),
    );
  });

  it("hands over a curl line that fails loudly and one that reports status", () => {
    render(<PushUrlReveal url={URL_} name="Nightly backup" />);
    const cron = (screen.getByLabelText(/for a cron line/i) as HTMLInputElement)
      .value;
    // -f so an HTTP error is a non-zero exit, -sS so a failure still prints.
    expect(cron).toContain("curl -fsS");
    expect(cron).toContain(URL_);
    const status = (
      screen.getByLabelText(/report failures too/i) as HTMLInputElement
    ).value;
    expect(status).toContain("?status=$?");
  });

  it("quotes the URL, so a shell cannot eat the query string", () => {
    // Unquoted, `?status=$?` is a glob and `$?` expands before curl sees it.
    expect(curlLine(URL_)).toContain(`"${URL_}"`);
    expect(curlStatusLine(URL_)).toContain(`"${URL_}?status=$?"`);
  });

  it("copies through the clipboard it was given", async () => {
    const writeClipboard = vi.fn().mockResolvedValue(undefined);
    render(
      <PushUrlReveal
        url={URL_}
        name="Nightly backup"
        writeClipboard={writeClipboard}
      />,
    );
    fireEvent.click(screen.getAllByRole("button", { name: /^copy$/i })[0]);
    await waitFor(() => expect(writeClipboard).toHaveBeenCalledWith(URL_));
  });
});

describe("a push monitor in the list and the detail view", () => {
  it("shows its window where a probed monitor shows its address", () => {
    // The alternative is a blank column, which reads as a monitor that failed
    // to save rather than as one with nothing to dial.
    expect(describeTarget(pushMonitor())).toMatch(/every 1 h/i);
    expect(
      describeTarget(pushMonitor({ push: undefined, target: "https://x" })),
    ).toBe("https://x");
  });

  it("explains the silence instead of calling it a missing check", () => {
    render(
      <MonitorDetail
        monitor={pushMonitor()}
        windows={[]}
        incidents={[]}
        now={1_700_000_000_000}
        beatWidth={720}
      />,
    );
    const text = document.body.textContent ?? "";
    expect(text).toMatch(/waiting/i);
    expect(text).toMatch(/push URL/i);
    expect(text).not.toMatch(/No checks recorded/i);
    // The window is the thing a push monitor is defined by, so it is on screen.
    expect(text).toMatch(/every 1 h/i);
    expect(text).toMatch(/5 min grace/i);
  });

  it("still says checks for a probed monitor", () => {
    render(
      <MonitorDetail
        monitor={pushMonitor({
          push: undefined,
          status: "pending",
          target: "https://x",
        })}
        windows={[]}
        incidents={[]}
        now={1_700_000_000_000}
        beatWidth={720}
      />,
    );
    expect(document.body.textContent ?? "").toMatch(/No checks recorded/i);
  });
});
