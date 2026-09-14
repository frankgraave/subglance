// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { Chart } from "./Chart";

afterEach(cleanup);

// Resolved from the working directory rather than from `import.meta.url`:
// under the jsdom environment the module URL is an http one, and
// `fileURLToPath` refuses it.
const chartCss = readFileSync(
  join(process.cwd(), "src", "components", "chart.css"),
  "utf8",
);

/** The stylesheet with its comments blanked, so prose cannot trip a scan. */
const rules = chartCss.replace(/\/\*[\s\S]*?\*\//g, "");

describe("the chart draws no axis furniture", () => {
  it("renders gridlines and nothing else behind the plot", () => {
    render(
      <Chart headline="99.98%">
        <div data-testid="marks" />
      </Chart>,
    );
    const grid = screen.getByTestId("chart-grid");
    expect(grid.querySelectorAll(".chart-gridline")).toHaveLength(3);
    // The gridlines say nothing a reader could act on, so they are out of the
    // accessibility tree entirely.
    expect(grid.getAttribute("aria-hidden")).toBe("true");
    expect(screen.getByTestId("marks")).toBeTruthy();
  });

  it("keeps every gridline off the plot's own edges", () => {
    // A line at 0% or 100% is an axis wearing a gridline's class, which is the
    // exact thing §13 removes.
    render(
      <Chart headline="99.98%" gridLines={4}>
        <div />
      </Chart>,
    );
    const tops = [...screen.getByTestId("chart-grid").children].map((line) =>
      Number.parseFloat((line as HTMLElement).style.top),
    );
    expect(tops).toHaveLength(4);
    for (const top of tops) {
      expect(top).toBeGreaterThan(0);
      expect(top).toBeLessThan(100);
    }
  });

  it("puts no border, outline or background on the plot", () => {
    // Asserted against the stylesheet rather than against computed style,
    // because jsdom applies no stylesheet — and this is the rule most likely
    // to be undone later by someone tidying the component up.
    const plot = rules.slice(rules.indexOf(".chart-plot {"));
    const body = plot.slice(0, plot.indexOf("}"));
    expect(body).not.toMatch(/border|outline|background/);
  });
});

describe("the chart states its window in the two bottom corners", () => {
  it("shows the start on the left and the end on the right", () => {
    render(
      <Chart headline="99.98%" start="14:02" end="15:41">
        <div />
      </Chart>,
    );
    const range = screen.getByTestId("chart-start").parentElement!;
    expect([...range.children].map((c) => c.textContent)).toEqual([
      "14:02",
      "15:41",
    ]);
  });

  it("keeps a lone end time anchored to its own corner", () => {
    // Both corners render even when one is empty, so a single time does not
    // slide to the middle and stop meaning "this end of the window".
    render(
      <Chart headline="99.98%" end="15:41">
        <div />
      </Chart>,
    );
    expect(screen.getByTestId("chart-start").textContent).toBe("");
    expect(screen.getByTestId("chart-end").textContent).toBe("15:41");
  });

  it("omits the range entirely when there is no window to state", () => {
    render(
      <Chart headline="99.98%">
        <div />
      </Chart>,
    );
    expect(screen.queryByTestId("chart-start")).toBeNull();
  });

  it("sets the corner times in the mono role, which slashes the zero", () => {
    // The point of mono here is not the shapes: `--numeric-mono` carries
    // slashed-zero and tabular figures, and `face-mono` is the only way to get
    // them. A rule that reached for the family alone would look identical and
    // render 0 and O the same.
    const time = rules.slice(rules.indexOf(".chart-time {"));
    expect(time.slice(0, time.indexOf("}"))).toContain("@apply face-mono;");
  });
});

describe("the chart leads with the number and follows with the split", () => {
  it("puts the headline and the breakdown on one line, breakdown right", () => {
    render(
      <Chart headline="99.98%" breakdown="412 checks · 2 failed">
        <div />
      </Chart>,
    );
    const head = screen.getByTestId("chart-headline").parentElement!;
    expect([...head.children].map((c) => c.textContent)).toEqual([
      "99.98%",
      "412 checks · 2 failed",
    ]);
    const body = rules.slice(rules.indexOf(".chart-head {"));
    expect(body.slice(0, body.indexOf("}"))).toContain(
      "justify-content: space-between;",
    );
  });

  it("renders the headline before the plot in document order", () => {
    // Reading order is the decision, and it has to hold for a screen reader
    // too — CSS could re-order this visually and leave the announcement
    // starting with the marks.
    render(
      <Chart headline="99.98%">
        <div />
      </Chart>,
    );
    const chart = screen.getByTestId("chart-headline").closest(".chart")!;
    const order = [...chart.children].map((c) => c.className);
    expect(order[0]).toBe("chart-head");
    expect(order[1]).toBe("chart-plot");
  });
});

describe("the legend is a slot below the plot", () => {
  it("renders whatever the caller puts in it, after the plot", () => {
    render(
      <Chart headline="99.98%" legend={<span>Bar height is latency</span>}>
        <div />
      </Chart>,
    );
    const chart = screen.getByTestId("chart-legend").closest(".chart")!;
    const children = [...chart.children].map((c) => c.className);
    expect(children.indexOf("chart-legend")).toBeGreaterThan(
      children.indexOf("chart-plot"),
    );
    expect(screen.getByText("Bar height is latency")).toBeTruthy();
  });

  it("draws no legend box when the caller supplies none", () => {
    // An empty bordered box is chrome standing in for content that is not
    // there, which is the corner legend this layout exists to avoid.
    render(
      <Chart headline="99.98%">
        <div />
      </Chart>,
    );
    expect(screen.queryByTestId("chart-legend")).toBeNull();
  });
});
