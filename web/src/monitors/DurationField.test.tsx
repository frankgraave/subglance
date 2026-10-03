// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { DurationField } from "./DurationField";
import { SECONDS_ONLY, SECONDS_TO_HOURS } from "./duration";
afterEach(cleanup);

function Controlled({ initial, onChange }: { initial: string; onChange?: (value: string) => void }) {
  const [value, setValue] = useState(initial);
  return <>
    <label htmlFor="d">Check every</label>
    <DurationField id="d" value={value} units={SECONDS_TO_HOURS} label="Check every"
      onChange={(next) => { setValue(next); onChange?.(next); }} />
    <button type="button" onClick={() => setValue("7200")}>Reload</button>
  </>;
}

const box = () => screen.getByLabelText("Check every") as HTMLInputElement;
const unit = () => screen.getByLabelText("Check every: unit") as HTMLSelectElement;

describe("DurationField", () => {
  it("names the unit picker after the field, and words the units for the amount", () => {
    render(<Controlled initial="60" />);
    expect(box().value).toBe("1");
    expect(unit().value).toBe("min");
    expect([...unit().options].map((o) => o.textContent)).toEqual(["second", "minute", "hour"]);
    fireEvent.change(box(), { target: { value: "5" } });
    expect([...unit().options].map((o) => o.textContent)).toEqual(["seconds", "minutes", "hours"]);
  });

  it("keeps what is typed on the way to a number", () => {
    const onChange = vi.fn();
    render(<Controlled initial="3600" onChange={onChange} />);
    fireEvent.change(box(), { target: { value: "1." } });
    expect(box().value).toBe("1.");
    fireEvent.change(box(), { target: { value: "1.5" } });
    expect(onChange).toHaveBeenLastCalledWith("5400");
    fireEvent.change(box(), { target: { value: "" } });
    expect(box().value).toBe("");
    expect(onChange).toHaveBeenLastCalledWith("");
  });

  it("takes a value that arrives from outside, unit and all", () => {
    render(<Controlled initial="900" />);
    expect(unit().value).toBe("min");
    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(box().value).toBe("2");
    expect(unit().value).toBe("h");
  });

  it("does not let a form that stores numbers overwrite an emptied box with 0", () => {
    function NumberForm() {
      const [n, setN] = useState(60);
      return <><label htmlFor="d">Check every</label>
        <DurationField id="d" value={String(n)} units={SECONDS_TO_HOURS} label="Check every" onChange={(v) => setN(Number(v))} /></>;
    }
    render(<NumberForm />);
    fireEvent.change(box(), { target: { value: "" } });
    expect(box().value).toBe("");
  });

  it("draws one unit as a fixed addon rather than a picker of one", () => {
    render(<><label htmlFor="t">Give up after</label>
      <DurationField id="t" value="10" units={SECONDS_ONLY} label="Give up after" onChange={vi.fn()} /></>);
    expect(screen.queryByRole("combobox")).toBeNull();
    expect((screen.getByLabelText("Give up after") as HTMLInputElement).value).toBe("10");
    expect(screen.getByText("sec").getAttribute("aria-hidden")).toBe("true");
  });
});
