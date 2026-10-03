// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Select } from "./Select";
import { FileInput } from "./FileInput";

afterEach(cleanup);

describe("Select", () => {
  it("is a native select, named by its label, with the caller's class first", () => {
    const onChange = vi.fn();
    render(
      <label>
        Expires
        <Select className="input input--inset" value="" onChange={onChange}>
          <option value="">Never</option>
          <option value="720h">30 days</option>
        </Select>
      </label>,
    );
    const select = screen.getByRole("combobox", { name: "Expires" }) as HTMLSelectElement;
    expect(select.tagName).toBe("SELECT");
    expect(select.className).toBe("input input--inset select");
    fireEvent.change(select, { target: { value: "720h" } });
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("draws the arrow on a select with no class of its own", () => {
    render(<Select aria-label="Unit"><option>minutes</option></Select>);
    expect(screen.getByRole("combobox", { name: "Unit" }).className).toBe("select");
  });

  it("keeps the value in a form", () => {
    const { container } = render(
      <form><Select name="scope" defaultValue="tag"><option value="monitor">One monitor</option><option value="tag">Tag group</option></Select></form>,
    );
    expect(new FormData(container.querySelector("form")!).get("scope")).toBe("tag");
  });
});

describe("FileInput", () => {
  it("is a native file input, named by its label", () => {
    const onChange = vi.fn();
    render(
      <>
        <label htmlFor="cfg">Configuration file</label>
        <FileInput id="cfg" accept=".yaml" onChange={onChange} />
      </>,
    );
    const input = screen.getByLabelText("Configuration file") as HTMLInputElement;
    expect(input.tagName).toBe("INPUT");
    expect(input.type).toBe("file");
    expect(input.accept).toBe(".yaml");
    expect(input.className).toBe("file-input");
  });
});
