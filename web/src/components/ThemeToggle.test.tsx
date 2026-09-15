// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ThemeToggle } from "./ThemeToggle";

afterEach(cleanup);

/*
 * The control is drawn as three glyphs, so the thing worth testing is the part
 * that is no longer visible: its name. An icon-only button whose label was
 * dropped along with the text is announced as "button", and this is exactly
 * the change that produces that bug.
 */
describe("ThemeToggle", () => {
  it("keeps a name on every segment after the labels became glyphs", () => {
    render(<ThemeToggle preference="system" onChange={() => {}} />);
    const names = screen
      .getAllByRole("button")
      .map((b) => b.getAttribute("aria-label"));
    expect(names).toEqual(["Light", "Dark", "Auto"]);
  });

  it("draws no text, so the bar costs three squares and not three words", () => {
    render(<ThemeToggle preference="light" onChange={() => {}} />);
    for (const button of screen.getAllByRole("button")) {
      expect(button.textContent).toBe("");
      expect(button.querySelector("svg")).not.toBeNull();
    }
  });

  it("tells you what Auto follows", () => {
    // "Auto" on its own reads as "follows the time of day" just as easily.
    render(<ThemeToggle preference="light" onChange={() => {}} />);
    expect(
      screen.getByRole("button", { name: "Auto" }).getAttribute("title"),
    ).toBe("Follow the operating system");
  });

  it("marks the current preference and reports a change", () => {
    const onChange = vi.fn();
    render(<ThemeToggle preference="dark" onChange={onChange} />);
    expect(
      screen.getByRole("button", { name: "Dark" }).getAttribute("aria-pressed"),
    ).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Light" }));
    expect(onChange).toHaveBeenCalledWith("light");
  });

  it("is still one named group, not three loose glyphs", () => {
    render(<ThemeToggle preference="system" onChange={() => {}} />);
    expect(screen.getByRole("group", { name: "Colour theme" })).toBeTruthy();
  });
});
