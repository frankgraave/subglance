// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ResponseHistory } from "./ResponseHistory";

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
    { id: "10", ts: "2026-09-19T12:00:00Z", ok: false },
    { id: "11", ts: "2026-09-19T12:01:00Z", ok: false, response: null, response_capture_reason: "disabled" },
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
