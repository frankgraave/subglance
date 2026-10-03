// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ErrorBoundary } from "../shell/ErrorBoundary";
import { ChunkedForm } from "./LazyMonitorForms";
import { retryableLazy } from "./retryableLazy";

/**
 * A form chunk that fails to load stays inside its drawer.
 *
 * The forms used to ship in the entry bundle and could not fail to arrive.
 * Behind `lazy()` they can, and a rejected import throws on render. This
 * holds the two promises the wrapper makes about that: the failure is shown
 * where the form would have been, not by a boundary further up, and
 * "Try again" asks for the chunk again rather than rethrowing the error
 * `lazy()` keeps from the first attempt. It drives `ChunkedForm`, which
 * `LazyAddMonitor` and `LazyEditMonitorForm` both render, with a loader that
 * fails once.
 */

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function Form({ name }: { name: string }) {
  return <p>form for {name}</p>;
}

it("shows a failed chunk inside the form's place and loads it again on retry", async () => {
  // React reports a caught render error to the console whatever catches it.
  vi.spyOn(console, "error").mockImplementation(() => {});
  let attempts = 0;
  const chunk = retryableLazy<{ name: string }>(() => {
    attempts += 1;
    return attempts === 1
      ? Promise.reject(new Error("Failed to fetch dynamically imported module"))
      : Promise.resolve({ default: Form });
  });

  render(
    <ErrorBoundary title="The whole screen broke." onError={() => {}}>
      <p>drawer title</p>
      <ChunkedForm chunk={chunk} props={{ name: "api" }} />
    </ErrorBoundary>,
  );

  const panel = await screen.findByRole("alert");
  expect(panel.textContent).toContain("The form could not be loaded.");
  expect(panel.textContent).toContain("Failed to fetch dynamically imported module");
  // The screen's boundary did not catch it, so the drawer around it stays.
  expect(screen.queryByText("The whole screen broke.")).toBeNull();
  expect(screen.getByText("drawer title")).toBeTruthy();
  // One failure is shown once; nothing retried behind the user's back.
  expect(attempts).toBe(1);

  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  expect(await screen.findByText("form for api")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(attempts).toBe(2);
});
