// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MonitorLink } from "./MonitorLink";

afterEach(cleanup);

const link = () => screen.getByText("api") as HTMLAnchorElement;

describe("MonitorLink", () => {
  it("is a real anchor with a shareable href", () => {
    render(<MonitorLink id="42" name="api" />);
    expect(link().tagName).toBe("A");
    expect(link().getAttribute("href")).toBe("/monitors/42");
  });

  it("encodes an id that would otherwise break the path", () => {
    render(<MonitorLink id="a/b" name="api" />);
    expect(link().getAttribute("href")).toBe("/monitors/a%2Fb");
  });

  it("routes client-side on a plain left click", () => {
    const onOpen = vi.fn();
    render(<MonitorLink id="42" name="api" onOpen={onOpen} />);
    const event = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 });
    fireEvent(link(), event);
    expect(onOpen).toHaveBeenCalledWith("42");
    expect(event.defaultPrevented).toBe(true);
  });

  it("stands aside for a modified click, so Cmd-click still opens a tab", () => {
    // The step hand-rolled SPA links usually skip. Calling preventDefault here
    // would silently break "open in new tab" for every monitor on the page.
    const onOpen = vi.fn();
    render(<MonitorLink id="42" name="api" onOpen={onOpen} />);
    for (const modifier of ["metaKey", "ctrlKey", "shiftKey", "altKey"] as const) {
      const event = new MouseEvent("click", {
        bubbles: true,
        cancelable: true,
        button: 0,
        [modifier]: true,
      });
      fireEvent(link(), event);
      expect(event.defaultPrevented).toBe(false);
    }
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("stands aside for a middle click", () => {
    const onOpen = vi.fn();
    render(<MonitorLink id="42" name="api" onOpen={onOpen} />);
    const event = new MouseEvent("click", { bubbles: true, cancelable: true, button: 1 });
    fireEvent(link(), event);
    expect(onOpen).not.toHaveBeenCalled();
    expect(event.defaultPrevented).toBe(false);
  });

  it("leaves the href working when no handler is given", () => {
    render(<MonitorLink id="42" name="api" />);
    const event = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 });
    fireEvent(link(), event);
    expect(event.defaultPrevented).toBe(false);
  });
});
