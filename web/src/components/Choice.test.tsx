// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Checkbox, Radio } from "./Choice";

afterEach(cleanup);

describe("Checkbox", () => {
  it("is a native checkbox, named by the words beside it", () => {
    const onChange = vi.fn();
    render(<Checkbox onChange={onChange}>Follow redirects</Checkbox>);
    const box = screen.getByRole("checkbox", { name: "Follow redirects" }) as HTMLInputElement;
    expect(box.tagName).toBe("INPUT");
    expect(box.type).toBe("checkbox");
    expect(box.classList.contains("choice")).toBe(true);
    // The words are the target: a click on them toggles the box.
    fireEvent.click(screen.getByText("Follow redirects"));
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(box.closest("label")?.className).toBe("choice-label");
  });

  it("renders the bare input when it has no words, for a row's selection box", () => {
    const view = render(<Checkbox aria-label="Select api" className="bulk-tags-select" />);
    const box = screen.getByRole("checkbox", { name: "Select api" });
    expect(box.closest("label")).toBeNull();
    expect(box.className).toBe("choice bulk-tags-select");
    expect(view.container.firstElementChild).toBe(box);
  });

  it("sets the mixed state as a property, and clears it again", () => {
    const view = render(<Checkbox aria-label="All" indeterminate />);
    const box = screen.getByRole("checkbox", { name: "All" }) as HTMLInputElement;
    expect(box.indeterminate).toBe(true);
    // Not an attribute: there is no HTML attribute for it, and React would
    // write `indeterminate=""`, which no browser reads.
    expect(box.hasAttribute("indeterminate")).toBe(false);
    view.rerender(<Checkbox aria-label="All" indeterminate={false} />);
    expect(box.indeterminate).toBe(false);
  });

  it("keeps its value in a form, which is what the maintenance weekdays read", () => {
    const view = render(
      <form>
        <Checkbox name="weekdays" value={0} defaultChecked>Mon</Checkbox>
        <Checkbox name="weekdays" value={1}>Tue</Checkbox>
        <Checkbox name="weekdays" value={2} defaultChecked>Wed</Checkbox>
      </form>,
    );
    const form = view.container.querySelector("form")!;
    expect(new FormData(form).getAll("weekdays")).toEqual(["0", "2"]);
  });
});

describe("Radio", () => {
  it("is a native radio, and two with one name are one group", () => {
    render(
      <>
        <Radio name="during" value="hold" defaultChecked>Hold</Radio>
        <Radio name="during" value="drop">Drop</Radio>
      </>,
    );
    const hold = screen.getByRole("radio", { name: "Hold" }) as HTMLInputElement;
    const drop = screen.getByRole("radio", { name: "Drop" }) as HTMLInputElement;
    expect(hold.checked).toBe(true);
    fireEvent.click(screen.getByText("Drop"));
    expect(drop.checked).toBe(true);
    expect(hold.checked).toBe(false);
  });
});
