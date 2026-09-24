// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { LatencyChart } from "./LatencyChart";
import { seriesFromApi, type ApiLatencyPoint } from "./latency";

const HOUR = 3_600_000;
const FROM = Date.parse("2026-09-20T00:00:00Z");

const point = (i: number, avg: number | null, over: Partial<ApiLatencyPoint> = {}): ApiLatencyPoint => ({
  t: new Date(FROM + i * HOUR).toISOString(),
  checks: 60,
  samples: avg === null ? 0 : 60,
  down: 0,
  avg_ms: avg,
  min_ms: avg === null ? null : avg - 5,
  max_ms: avg === null ? null : avg + 5,
  ...over,
});

const makeSeries = (points: ApiLatencyPoint[], window = "24h") =>
  seriesFromApi({
    window,
    step_s: 3600,
    from: new Date(FROM).toISOString(),
    to: new Date(FROM + 24 * HOUR).toISOString(),
    points,
  });

afterEach(cleanup);

describe("LatencyChart", () => {
  it("states the weighted average, the peak and the check count", () => {
    render(
      <LatencyChart
        window="24h"
        onWindowChange={() => {}}
        series={makeSeries([point(0, 100), point(1, 300)])}
        width={400}
      />,
    );
    expect(screen.getByTestId("chart-headline").textContent).toBe("200 ms");
    expect(screen.getByTestId("chart-breakdown").textContent).toBe("peak 300 ms · 120 checks");
  });

  it("breaks the line at an outage and marks the outage on the baseline", () => {
    render(
      <LatencyChart
        window="24h"
        onWindowChange={() => {}}
        series={makeSeries([point(0, 100), point(1, 120), point(2, null, { down: 1, checks: 1 }), point(3, 110)])}
        width={400}
      />,
    );
    expect(screen.getAllByTestId("lat-run")).toHaveLength(2);
    expect(screen.getAllByTestId("lat-down")).toHaveLength(1);
    expect(screen.getByTestId("chart-breakdown").textContent).toContain("down in 1 step");
  });

  it("offers the windows as one pressed choice and reports a change", () => {
    const onWindowChange = vi.fn();
    render(<LatencyChart window="24h" onWindowChange={onWindowChange} series={makeSeries([point(0, 10)])} />);
    const group = screen.getByRole("group", { name: "Latency window" });
    expect(within(group).getByRole("button", { name: "24h" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(within(group).getByRole("button", { name: "7d" }));
    expect(onWindowChange).toHaveBeenCalledWith("7d");
  });

  it("reads a step out on keyboard focus and says when nothing was measured", () => {
    render(
      <LatencyChart
        window="24h"
        onWindowChange={() => {}}
        series={makeSeries([point(0, 100), point(1, null, { down: 60 })])}
        width={400}
      />,
    );
    const plot = screen.getByTestId("lat-plot");
    fireEvent.keyDown(plot, { key: "ArrowRight" });
    const tooltip = screen.getByTestId("lat-tooltip");
    expect(tooltip.textContent).toContain("100 ms");
    expect(tooltip.textContent).toContain("95 ms");
    fireEvent.keyDown(plot, { key: "ArrowRight" });
    expect(screen.getByTestId("lat-tooltip").textContent).toContain("not measured");
    expect(screen.getByTestId("lat-tooltip").textContent).toContain("60 confirmed down");
    fireEvent.keyDown(plot, { key: "Escape" });
    expect(screen.queryByTestId("lat-tooltip")).toBeNull();
  });

  it("gives a screen reader every step as a table row", () => {
    render(
      <LatencyChart window="24h" onWindowChange={() => {}} series={makeSeries([point(0, 100), point(5, null)])} />,
    );
    const rows = screen.getAllByRole("row");
    // Header plus one row per step.
    expect(rows).toHaveLength(3);
    expect(rows[2].textContent).toContain("not measured");
  });

  it("says an empty window is empty rather than drawing a flat line", () => {
    render(<LatencyChart window="7d" onWindowChange={() => {}} series={makeSeries([])} />);
    expect(screen.getByText("No checks in the last 7d.")).toBeTruthy();
    expect(screen.queryByTestId("lat-plot")).toBeNull();
  });

  it("keeps the previous window on screen, dimmed, while the next one loads", () => {
    const { container } = render(
      <LatencyChart window="7d" onWindowChange={() => {}} series={makeSeries([point(0, 10)])} refreshing />,
    );
    const figure = container.querySelector(".lat-figure")!;
    expect(figure.hasAttribute("data-refreshing")).toBe(true);
    expect(figure.getAttribute("aria-busy")).toBe("true");
    expect(screen.queryByText("Loading latency…")).toBeNull();
  });

  it("names a load failure, and keeps a loaded window when only a refresh failed", () => {
    render(<LatencyChart window="24h" onWindowChange={() => {}} error={new Error("HTTP 500")} />);
    expect(screen.getByRole("alert").textContent).toBe("Could not load latency: HTTP 500");
    cleanup();
    render(
      <LatencyChart window="24h" onWindowChange={() => {}} series={makeSeries([point(0, 10)])} error={new Error("HTTP 500")} />,
    );
    expect(screen.getByTestId("lat-plot")).toBeTruthy();
    expect(screen.getByRole("alert").textContent).toContain("Showing the last loaded window");
  });

  it("keeps the keyboard readout on its step when the same window is refetched", () => {
    const { rerender } = render(
      <LatencyChart window="24h" onWindowChange={() => {}} series={makeSeries([point(0, 100), point(1, 200)])} width={400} />,
    );
    const plot = screen.getByTestId("lat-plot");
    fireEvent.keyDown(plot, { key: "ArrowRight" });
    fireEvent.keyDown(plot, { key: "ArrowRight" });
    expect(screen.getByTestId("lat-tooltip").textContent).toContain("200 ms");
    // A refetch returns a new object with one more step in front.
    rerender(
      <LatencyChart
        window="24h"
        onWindowChange={() => {}}
        series={makeSeries([point(-1, 50), point(0, 100), point(1, 200)])}
        width={400}
      />,
    );
    expect(screen.getByTestId("lat-tooltip").textContent).toContain("200 ms");
    rerender(
      <LatencyChart window="7d" onWindowChange={() => {}} series={makeSeries([point(0, 100), point(1, 200)])} width={400} />,
    );
    expect(screen.queryByTestId("lat-tooltip")).toBeNull();
  });

  it("describes the window on screen, not the one still loading", () => {
    render(
      <LatencyChart window="7d" onWindowChange={() => {}} series={makeSeries([point(0, 10)], "24h")} refreshing />,
    );
    expect(screen.getByTestId("lat-plot").getAttribute("aria-label")).toContain("over the last 24h");
    cleanup();
    render(<LatencyChart window="7d" onWindowChange={() => {}} series={makeSeries([], "24h")} refreshing />);
    expect(screen.getByText("Loading latency…")).toBeTruthy();
    expect(screen.queryByText("No checks in the last 7d.")).toBeNull();
  });
});
