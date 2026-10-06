// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ResponseHistory } from "./ResponseHistory";
import type { ResponseHeartbeat } from "./responseHistoryModel";

afterEach(cleanup);

it.each(["2026-09-19T12:00:00Z", "2026-09-19T12:01:00Z"])(
  "preserves the disclosed check's DOM, focus and scroll when %s is prepended",
  (ts) => {
    const existing = ["2", "1"].map((id) => ({
      id, ts: "2026-09-19T12:00:00Z", ok: false, response: { body: "identical failure" },
    }));
    const { container, rerender } = render(<ResponseHistory heartbeats={existing} />);
    const disclosure = container.querySelector("details")!;
    const body = container.querySelector("pre")!;
    fireEvent.click(disclosure.querySelector("summary")!);
    body.focus();
    body.scrollTop = 80;
    // Refetch creates new objects, not just a new array of the previous refs.
    rerender(<ResponseHistory heartbeats={JSON.parse(JSON.stringify([
      { ...existing[0], id: "3", ts }, ...existing,
    ]))} />);
    const disclosures = container.querySelectorAll("details");
    expect(disclosures).toHaveLength(3);
    expect(disclosures[0].open).toBe(false);
    expect(disclosures[1].open).toBe(true);
    expect(disclosures[2].open).toBe(false);
    expect(disclosures[1]).toBe(disclosure);
    expect(document.activeElement).toBe(body);
    expect(body.scrollTop).toBe(80);
  },
);

it("shows truncation visibly and only the capture allowlist of headers", () => {
  const { container } = render(<ResponseHistory heartbeats={[{
    id: "10", ts: "2026-09-19T12:00:00Z", ok: false,
    response: { body: "partial", truncated: true, headers: {
      "Content-Type": "text/plain", "Retry-After": "120", "x-request-id": "trace-1",
      "Set-Cookie": "session=secret", Authorization: "Bearer secret", "X-Unlisted": "private",
    } },
  }]} />);
  expect(screen.getByText("Truncated — only the beginning of the response was captured.")).toBeTruthy();
  fireEvent.click(screen.getByText("Captured response"));
  expect(screen.getByText("120")).toBeTruthy();
  expect(screen.getByText("trace-1")).toBeTruthy();
  expect(container.textContent).not.toContain("secret");
  expect(container.textContent).not.toContain("private");
});

it("distinguishes a stored empty response body from an absent response", () => {
  const { container } = render(<ResponseHistory heartbeats={[{
    id: "10", ts: "2026-09-19T12:00:00Z", ok: false, response: { body: "" },
  }]} />);
  expect(container.querySelector("details")).not.toBeNull();
  fireEvent.click(screen.getByText("Captured response"));
  expect(screen.getByText("The captured response body was empty.")).toBeTruthy();
  expect(screen.queryByText(/Truncated/)).toBeNull();
});

it("keeps failed-check time, status and error beside the diagnostic", () => {
  const { container } = render(<ResponseHistory heartbeats={[{
    id: "10", ts: "2026-09-19T12:00:00Z", ok: false, status_code: 503, error: "upstream unavailable",
  }]} />);
  expect(container.querySelector("time")?.getAttribute("dateTime")).toBe("2026-09-19T12:00:00Z");
  expect(screen.getByText("HTTP 503")).toBeTruthy();
  expect(screen.getByText("upstream unavailable")).toBeTruthy();
});

it("does not turn loading or a failed history request into an empty-history claim", () => {
  const { rerender } = render(<ResponseHistory heartbeats={[]} loading />);
  expect(screen.getByRole("status").textContent).toContain("Loading failure responses");
  expect(screen.queryByText(/No failed checks/)).toBeNull();
  rerender(<ResponseHistory heartbeats={[]} error={new Error("HTTP 503")} />);
  expect(screen.getByRole("alert").textContent).toContain("Could not load failure responses");
  expect(screen.queryByText(/No failed checks/)).toBeNull();
  rerender(<ResponseHistory heartbeats={[]} />);
  expect(screen.getByText("No failed checks in the recent history.")).toBeTruthy();
});


it("never draws an empty disclosure for missing snapshots or successful checks", () => {
  const { container } = render(<ResponseHistory heartbeats={[
    // Two different errors, so the two failures stay two rows and each states
    // its own reason in the single-check wording.
    { id: "10", ts: "2026-09-19T12:00:00Z", ok: false, error: "first" },
    { id: "11", ts: "2026-09-19T12:01:00Z", ok: false, error: "second", response: null, response_capture_reason: "disabled" },
    { id: "12", ts: "2026-09-19T12:02:00Z", ok: true, response: { body: "must not show" } },
  ]} />);
  expect(container.querySelector("details, pre")).toBeNull();
  expect(screen.getByText("Capture was switched off for this check.")).toBeTruthy();
  expect(screen.getByText("No captured response. Reason not recorded.")).toBeTruthy();
  expect(screen.queryByText("must not show")).toBeNull();
});

it.each([
  ["flapping", "Capture stopped while this monitor was flapping; this check's response was not stored."],
  ["budget", "This incident's response capture allowance had been used; this check's response was not stored."],
] as const)("explains the persisted %s decision without a disclosure", (reason, message) => {
  const { container } = render(<ResponseHistory heartbeats={[
    { id: "10", ts: "2026-09-19T12:00:00Z", ok: false, response_capture_reason: reason },
  ]} />);
  expect(screen.getByText(message)).toBeTruthy();
  expect(container.querySelector("details")).toBeNull();
});


it("keeps a failed response collapsed and renders hostile markup only as text", () => {
  const body = '<script>window.attacked = true</script>\n<img src=x onerror=alert(1)>\n[link](javascript:alert(1))';
  const { container } = render(<ResponseHistory heartbeats={[{
    id: "10", ts: "2026-09-19T12:00:00Z", ok: false, status_code: 503, error: "status 503",
    response: { body, headers: { "Content-Type": "text/html", "X-Request-Id": "<b>request</b>" } },
  }]} />);
  const disclosure = container.querySelector("details");
  expect(disclosure).not.toBeNull();
  expect(disclosure?.open).toBe(false);
  fireEvent.click(screen.getByText("Captured response"));
  expect(disclosure?.open).toBe(true);
  expect(container.querySelector("pre")?.textContent).toBe(body);
  expect(screen.getByText("text/html")).toBeTruthy();
  expect(screen.getByText("<b>request</b>")).toBeTruthy();
  expect(container.querySelector("script, img, a, b")).toBeNull();
});

it.each([["status", "unexpected status code"], ["dns", "DNS failure"], ["new-kind", "new-kind"]])(
  "explains failure kind %s in operator language", (kind, words) => {
    render(<ResponseHistory heartbeats={[{ id: "1", ts: "2026-09-21T00:00:00Z", ok: false, failure_kind: kind }]} />);
    expect(screen.getByText(words)).toBeTruthy();
  },
);

describe("consecutive identical failures (SUB-184)", () => {
  // Newest first, a minute apart, as the API returns them.
  const same = (count: number, over: Partial<ResponseHeartbeat> = {}, start = 0): ResponseHeartbeat[] =>
    Array.from({ length: count }, (_, i) => ({
      id: String(1000 - start - i),
      ts: new Date(Date.parse("2026-09-19T14:00:00Z") - (start + i) * 60_000).toISOString(),
      ok: false,
      assessment: "down" as const,
      failure_kind: "status",
      status_code: 502,
      error: "unexpected status 502",
      ...over,
    }));

  it("tells forty identical failures in one row with their count and span", () => {
    const { container } = render(<ResponseHistory heartbeats={same(40)} />);
    const rows = container.querySelectorAll(".response-history-beat");
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("40 checks in a row");
    // The span runs from the oldest check to the newest, machine-readably.
    const times = [...rows[0].querySelectorAll(".response-history-check time")].map((t) => t.getAttribute("dateTime"));
    expect(times).toEqual(["2026-09-19T13:21:00.000Z", "2026-09-19T14:00:00.000Z"]);
    // The error and the missing-capture reason are said once, not forty times.
    expect(screen.getAllByText("unexpected status 502")).toHaveLength(1);
    expect(screen.getByText("No captured response for 40 of these checks. Reason not recorded.")).toBeTruthy();
    expect(screen.queryByText("No captured response. Reason not recorded.")).toBeNull();
  });

  it("starts a new row when anything the row prints differs", () => {
    // Each run differs from the plain one beside it in exactly one field, so
    // every field is what splits on its own.
    const changes: Partial<ResponseHeartbeat>[] = [
      { status_code: 503 },
      { error: "connection reset" },
      { assessment: "warning" },
      { maintenance: true },
      { failure_kind: "timeout" },
    ];
    const heartbeats = changes.flatMap((change, i) => [
      ...same(2, {}, i * 4),
      ...same(2, change, i * 4 + 2),
    ]);
    const { container } = render(<ResponseHistory heartbeats={heartbeats} />);
    expect([...container.querySelectorAll(".response-history-beat")].map((row) => row.getAttribute("data-count")))
      .toEqual(Array(10).fill("2"));
  });

  it("keeps two outages apart when a passed check stands between them", () => {
    const [a, b, c, d] = same(4);
    const { container } = render(<ResponseHistory heartbeats={[a, b, { ...c, ok: true }, d]} />);
    expect([...container.querySelectorAll(".response-history-beat")].map((row) => row.getAttribute("data-count")))
      .toEqual(["2", "1"]);
  });

  it("keeps every captured response, each behind its own disclosure", () => {
    const beats = same(5);
    beats[0] = { ...beats[0], response: { body: "newest body" } };
    beats[3] = { ...beats[3], response: { body: "older body" } };
    beats[4] = { ...beats[4], response_capture_reason: "budget" };
    const { container } = render(<ResponseHistory heartbeats={beats} />);
    expect(container.querySelectorAll(".response-history-beat")).toHaveLength(1);
    expect(container.querySelectorAll("details")).toHaveLength(2);
    expect([...container.querySelectorAll("pre")].map((pre) => pre.textContent)).toEqual(["newest body", "older body"]);
    // The checks without a response are counted by the reason recorded for them.
    expect(screen.getByText("No captured response for 2 of these checks. Reason not recorded.")).toBeTruthy();
    expect(screen.getByText("This incident's response capture allowance had been used; no response was stored for 1 of these checks.")).toBeTruthy();
  });

  it("keeps an open response's element while the run grows and slides", () => {
    // A long outage: every poll adds a failure at the front and, past the
    // hundred-check window, drops one at the back. No member of the run is on
    // screen for the whole outage, and the open disclosure must survive that.
    // IDs grow with time, newest first.
    const first = same(6).map((hb, i) => ({ ...hb, id: String(20 - i) }));
    first[3] = { ...first[3], response: { body: "kept open" } };
    const { container, rerender } = render(<ResponseHistory heartbeats={first} />);
    const disclosure = container.querySelector("details")!;
    fireEvent.click(disclosure.querySelector("summary")!);
    expect(disclosure.open).toBe(true);
    // Next poll: id 21 is new at the front, id 15 has aged out at the back.
    const next = [{ ...first[0], id: "21" }, ...first.slice(0, 5).map((hb) => JSON.parse(JSON.stringify(hb)))];
    rerender(<ResponseHistory heartbeats={next} />);
    // And again: neither end of the first render's run is on screen now.
    const after = [{ ...first[0], id: "22" }, ...next.slice(0, 5).map((hb) => JSON.parse(JSON.stringify(hb)))];
    rerender(<ResponseHistory heartbeats={after} />);
    expect(container.querySelectorAll(".response-history-beat")).toHaveLength(1);
    expect(container.querySelector("details")).toBe(disclosure);
    expect(disclosure.open).toBe(true);
  });

  it("keeps an open response's element when a lone failure becomes a run", () => {
    const [only] = same(1);
    const first = { ...only, id: "50", response: { body: "lone body" } };
    const { container, rerender } = render(<ResponseHistory heartbeats={[first]} />);
    const disclosure = container.querySelector("details")!;
    fireEvent.click(disclosure.querySelector("summary")!);
    rerender(<ResponseHistory heartbeats={[{ ...first, id: "51", response: null }, { ...first }]} />);
    expect(container.querySelector(".response-history-beat")?.getAttribute("data-count")).toBe("2");
    expect(container.querySelector("details")).toBe(disclosure);
    expect(disclosure.open).toBe(true);
  });
});
