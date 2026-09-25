// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ChannelForm } from "./ChannelForm";
import { channelFromApi } from "./channels";
import { draftFrom, quietChange, quietProblem } from "./quietHours";

/*
 * Quiet hours in the channel form (SUB-124).
 *
 * The two promises tested here are the ones that decide whether an alert can
 * be lost: hold is what you get unless you choose otherwise, and saving a
 * channel whose window did not change does not touch the window — because the
 * server releases a held night's alerts whenever a window is replaced.
 */

afterEach(cleanup);

const NIGHT = {
  start: "23:00",
  end: "07:00",
  timezone: "Europe/Amsterdam",
  during: "hold",
};

function email(quiet: typeof NIGHT | null = null) {
  return channelFromApi({
    id: 3,
    name: "Ops email",
    type: "email",
    config: { to: "ops@example.com" },
    enabled: true,
    quiet_hours: quiet,
  });
}

describe("quietChange", () => {
  it("sends nothing when the stored window is unchanged", () => {
    expect(quietChange(NIGHT, draftFrom(NIGHT))).toBeUndefined();
  });

  it("sends nothing for a channel that never had a window", () => {
    expect(quietChange(null, draftFrom(null))).toBeUndefined();
  });

  it("removes the window when it is switched off", () => {
    expect(quietChange(NIGHT, { ...draftFrom(NIGHT), enabled: false })).toBeNull();
  });

  it("sends the whole window when any part of it changed", () => {
    expect(quietChange(NIGHT, { ...draftFrom(NIGHT), during: "drop" })).toEqual({
      ...NIGHT,
      during: "drop",
    });
    expect(
      quietChange(NIGHT, { ...draftFrom(NIGHT), timezone: " Europe/London " }),
    ).toEqual({ ...NIGHT, timezone: "Europe/London" });
  });
});

describe("draftFrom", () => {
  it("starts a new window on hold, never on drop", () => {
    const draft = draftFrom(null);
    expect(draft.enabled).toBe(false);
    expect(draft.during).toBe("hold");
    expect(draft.timezone).not.toBe("");
  });

  it("keeps a mode this build does not know rather than rewriting it", () => {
    expect(draftFrom({ ...NIGHT, during: "page" }).during).toBe("page");
  });
});

describe("quietProblem", () => {
  it("accepts a window that runs past midnight", () => {
    expect(quietProblem({ enabled: true, ...NIGHT })).toBeNull();
  });

  it("says nothing about a window that is off", () => {
    expect(quietProblem({ enabled: false, ...NIGHT, start: "" })).toBeNull();
  });

  it("refuses what the server would refuse", () => {
    expect(quietProblem({ enabled: true, ...NIGHT, start: "" })).toMatch(/start and an end/);
    expect(quietProblem({ enabled: true, ...NIGHT, end: "23:00" })).toMatch(/same minute/);
    expect(quietProblem({ enabled: true, ...NIGHT, timezone: "Local" })).toMatch(/named timezone/);
    expect(quietProblem({ enabled: true, ...NIGHT, timezone: "Mars/Olympus" })).toMatch(
      /not a timezone/,
    );
  });
});

describe("ChannelForm quiet hours", () => {
  it("adds a window on hold unless drop is chosen", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ops" } });
    fireEvent.change(screen.getByLabelText(/recipient address/i), {
      target: { value: "ops@example.com" },
    });
    fireEvent.click(screen.getByRole("checkbox", { name: /hold this channel's alerts/i }));
    expect((screen.getByRole("radio", { name: /^Hold/ }) as HTMLInputElement).checked).toBe(true);
    fireEvent.change(screen.getByLabelText("Timezone"), {
      target: { value: "Europe/Amsterdam" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^Add channel$/ }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][1]).toEqual({
      start: "22:00",
      end: "07:00",
      timezone: "Europe/Amsterdam",
      during: "hold",
    });
  });

  it("leaves a stored window alone when only the name changed", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={email(NIGHT)} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Ops list" } });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].name).toBe("Ops list");
    expect(onSave.mock.calls[0][1]).toBeUndefined();
  });

  it("switches drop on, and back off, as the mode", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={email(NIGHT)} onSave={onSave} />);
    fireEvent.click(screen.getByRole("radio", { name: /^Drop/ }));
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][1]).toEqual({ ...NIGHT, during: "drop" });
  });

  it("removes the window when the box is cleared", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={email(NIGHT)} onSave={onSave} />);
    expect(screen.getByText(/sends whatever it is holding right away/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("checkbox", { name: /hold this channel's alerts/i }));
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][1]).toBeNull();
  });

  it("refuses a window the server would refuse, before anything is saved", () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={email(NIGHT)} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "Local" } });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toMatch(/named timezone/);
  });
});
