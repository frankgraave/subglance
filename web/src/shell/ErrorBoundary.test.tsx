// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ErrorBoundary } from "./ErrorBoundary";

/**
 * SUB-99, part 2: a component that throws while rendering must leave a
 * visible recovery panel, not a blank page.
 *
 * React logs a caught error to `console.error` regardless of the boundary, so
 * each test silences it; a failing assertion still reports normally.
 */

function Boom({ fail }: { fail: boolean }) {
  if (fail) throw new Error("beats is not iterable");
  return <p>the dashboard</p>;
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("ErrorBoundary", () => {
  it("replaces a crashed subtree with an alert instead of nothing", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onError = vi.fn();

    render(
      <ErrorBoundary onError={onError}>
        <Boom fail />
      </ErrorBoundary>,
    );

    // The panel is announced, so a screen reader is told the page stopped
    // working rather than being left on a silent, empty document.
    const panel = screen.getByRole("alert");
    expect(panel.textContent).toContain("Something broke");
    // The detail is on screen, not only in the console.
    expect(panel.textContent).toContain("beats is not iterable");
    expect(screen.queryByText("the dashboard")).toBeNull();
    expect(onError).toHaveBeenCalledTimes(1);
  });

  it("renders its children untouched when nothing throws", () => {
    render(
      <ErrorBoundary>
        <Boom fail={false} />
      </ErrorBoundary>,
    );
    expect(screen.getByText("the dashboard")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("offers a retry that re-renders, and a reload for when it does not help", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onReload = vi.fn();
    let fail = true;

    function Flaky() {
      return <Boom fail={fail} />;
    }

    const view = render(
      <ErrorBoundary onError={() => {}} onReload={onReload}>
        <Flaky />
      </ErrorBoundary>,
    );
    screen.getByRole("alert");

    fireEvent.click(screen.getByRole("button", { name: "Reload the page" }));
    expect(onReload).toHaveBeenCalledTimes(1);

    // Retry only helps once whatever threw stops throwing, which is why both
    // buttons exist: the panel cannot know which of the two the user needs.
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    view.rerender(
      <ErrorBoundary onError={() => {}} onReload={onReload}>
        <Flaky />
      </ErrorBoundary>,
    );
    expect(screen.getByText("the dashboard")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
