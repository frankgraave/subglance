// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { ChannelForm } from "./ChannelForm";
import { channelFromApi } from "./channels";

/*
 * The add/edit form.
 *
 * The assertions that matter here are all about the credential: that a stored
 * one is never rendered, that a control accepting one is a password field, and
 * that saving without touching it leaves it alone — which on this API means
 * echoing the mask back exactly as it arrived.
 */

function slack() {
  return channelFromApi({
    id: 1,
    name: "On-call Slack",
    type: "slack",
    config: { url: "****B07F" },
    enabled: true,
  });
}

afterEach(cleanup);

describe("ChannelForm", () => {
  it("never renders a stored secret and offers no reveal", () => {
    // A reveal button is a leak with an extra click.
    render(<ChannelForm channel={slack()} onSave={async () => {}} />);
    expect(screen.queryByRole("button", { name: /reveal|show/i })).toBeNull();
    // No input holds the value at all: there is nothing to edit until Replace.
    for (const input of screen.queryAllByRole("textbox")) {
      expect((input as HTMLInputElement).value).not.toContain("hooks.slack");
    }
    expect(screen.getByText(/never sent back to this page/i)).toBeTruthy();
  });

  it("does not point a label at a control that is not there", () => {
    /*
     * While a stored secret is shown this field has no input — the only
     * control is Replace. A `<label for>` aimed at the missing input is worse
     * than no label: a screen reader reads the field name as loose text and
     * clicking it does nothing, and both failures look correct in the markup.
     *
     * Asserted through the accessibility tree rather than by inspecting
     * attributes: `getByLabelText` only resolves if a control really is
     * labelled, which is the property that matters.
     */
    const { container } = render(
      <ChannelForm channel={slack()} onSave={async () => {}} />,
    );
    const dangling = [...container.querySelectorAll("label[for]")]
      .map((label) => label.getAttribute("for") ?? "")
      .filter((target) => container.querySelector(`#${CSS.escape(target)}`) === null);
    // Listed rather than counted: a failure names the field that is broken.
    expect(dangling).toEqual([]);
    // And the one control that is there names the field it replaces, so it is
    // distinguishable from the Replace button of any other field.
    expect(screen.getByRole("button", { name: /^Replace / })).toBeTruthy();
  });

  it("sends the mask back unchanged when the secret is not touched", async () => {
    // handleUpdateChannel restores the stored credential when it sees a value
    // that still equals its own mask. An empty string would wipe it; omitting
    // the key would fail validation, since `url` is Slack's required field.
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={slack()} onSave={onSave} />);
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({
      name: "On-call Slack",
      type: "slack",
      config: { url: "****B07F" },
    });
  });

  it("takes a new secret through Replace, in a password field", async () => {
    // password while it accepts input: this page is opened on shared screens
    // and during screen shares, and the value stays visible until save.
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={slack()} onSave={onSave} />);
    fireEvent.click(
      screen.getByRole("button", { name: /replace incoming webhook url/i }),
    );
    const field = document.querySelector(
      'input[type="password"]',
    ) as HTMLInputElement;
    expect(field).toBeTruthy();
    fireEvent.change(field, {
      target: { value: "https://hooks.slack.com/services/T/B/new" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].config.url).toBe(
      "https://hooks.slack.com/services/T/B/new",
    );
  });

  it("can back out of a replacement and keep the stored value", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={slack()} onSave={onSave} />);
    fireEvent.click(
      screen.getByRole("button", { name: /replace incoming webhook url/i }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: /keep the stored incoming webhook url/i }),
    );
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].config.url).toBe("****B07F");
  });

  it("uses a password field for a brand-new secret too", () => {
    render(<ChannelForm onSave={async () => {}} />);
    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "telegram" },
    });
    const token = document.querySelector(
      'input[type="password"]',
    ) as HTMLInputElement;
    expect(token).toBeTruthy();
  });

  it("swaps the field set when the type changes", () => {
    render(<ChannelForm onSave={async () => {}} />);
    expect(screen.getByLabelText(/recipient address/i)).toBeTruthy();
    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "telegram" },
    });
    expect(screen.queryByLabelText(/recipient address/i)).toBeNull();
    expect(screen.getByLabelText(/chat id/i)).toBeTruthy();
  });

  it("refuses to change the type of an existing channel, and says why", () => {
    // PUT accepts a type change, but the field set changes with it — a Slack
    // channel turned into an e-mail one would carry a masked webhook URL into
    // a form with no box for it.
    render(<ChannelForm channel={slack()} onSave={async () => {}} />);
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.getByText(/cannot be changed here/i)).toBeTruthy();
  });

  it("refuses an empty name before sending anything", async () => {
    const onSave = vi.fn();
    render(<ChannelForm onSave={onSave} />);
    fireEvent.click(screen.getByRole("button", { name: /add channel/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toMatch(/needs a name/i);
  });

  it("refuses a missing required field, naming it", async () => {
    const onSave = vi.fn();
    render(<ChannelForm onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "Ops" },
    });
    fireEvent.click(screen.getByRole("button", { name: /add channel/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toMatch(
      /recipient address is required/i,
    );
  });

  it("shows the server's own rejection sentence", async () => {
    const onSave = vi
      .fn()
      .mockRejectedValue(new Error("config.url must use http or https"));
    render(<ChannelForm channel={slack()} onSave={onSave} />);
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toMatch(
        /must use http or https/,
      ),
    );
  });

  it("offers no field the notifier does not read", () => {
    // The mockup draws a Slack channel label, an HTTP method and a signing
    // secret. Saving any of them would store a value nothing would ever use.
    render(<ChannelForm onSave={async () => {}} />);
    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "webhook" },
    });
    expect(screen.queryByLabelText(/method/i)).toBeNull();
    expect(screen.queryByLabelText(/signing secret/i)).toBeNull();
    fireEvent.change(screen.getByRole("combobox"), {
      target: { value: "slack" },
    });
    expect(screen.queryByLabelText(/channel label/i)).toBeNull();
  });
});

function sms(numbers: string, over: Record<string, string> = {}) {
  return channelFromApi({
    id: 2,
    name: "On-call phones",
    type: "sms",
    config: {
      provider: "android-gateway",
      numbers,
      url: "****8080",
      username: "sms",
      password: "****word",
      recoveries: "false",
      ...over,
    },
    enabled: true,
  });
}

describe("ChannelForm, SMS", () => {
  const MASKED = "+31 6 \u2022\u2022\u2022\u2022 5678, +31 6 \u2022\u2022\u2022\u2022 4321";

  it("sends only the chosen provider's fields, numbers as one list", async () => {
    // A hidden field is not sent: a channel set up for Twilio must not also
    // store the gateway fields that were on screen before the switch.
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Type"), { target: { value: "sms" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Phones" } });
    expect(screen.getByLabelText("Gateway address")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Gateway username"), {
      target: { value: "sms" },
    });
    fireEvent.change(screen.getByLabelText("Sent through"), {
      target: { value: "twilio" },
    });
    expect(screen.queryByLabelText("Gateway address")).toBeNull();
    fireEvent.change(screen.getByLabelText("Phone numbers"), {
      target: { value: "06 1234 5678\n\n+44 7700 900123\n" },
    });
    fireEvent.change(screen.getByLabelText(/^Country code/), {
      target: { value: "+31" },
    });
    fireEvent.change(screen.getByLabelText("Account SID"), {
      target: { value: "AC123" },
    });
    fireEvent.change(screen.getByLabelText("Auth token"), {
      target: { value: "tok" },
    });
    fireEvent.change(screen.getByLabelText("Sender"), {
      target: { value: "SubGlance" },
    });
    fireEvent.click(screen.getByRole("button", { name: /add channel/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0]).toEqual({
      name: "Phones",
      type: "sms",
      config: {
        provider: "twilio",
        numbers: "06 1234 5678, +44 7700 900123",
        country_code: "+31",
        account_sid: "AC123",
        auth_token: "tok",
        from: "SubGlance",
        recoveries: "true",
      },
    });
  });

  it("asks for the numbers before anything is sent", () => {
    const onSave = vi.fn();
    render(<ChannelForm onSave={onSave} />);
    fireEvent.change(screen.getByLabelText("Type"), { target: { value: "sms" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Phones" } });
    fireEvent.change(screen.getByLabelText("Phone numbers"), {
      target: { value: " \n " },
    });
    fireEvent.click(screen.getByRole("button", { name: /add channel/i }));
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe(
      "Phone numbers is required for an SMS channel.",
    );
  });

  it("does not carry a value typed for one type into another", () => {
    // `url` is an ntfy server and an SMS gateway: the same key, not the
    // same setting.
    render(<ChannelForm onSave={async () => {}} />);
    fireEvent.change(screen.getByLabelText("Type"), { target: { value: "sms" } });
    fireEvent.change(screen.getByLabelText("Gateway username"), {
      target: { value: "gateway-user" },
    });
    fireEvent.change(screen.getByLabelText("Type"), { target: { value: "ntfy" } });
    expect((screen.getByLabelText(/^Username/) as HTMLInputElement).value).toBe("");
  });

  it("gives an administrator the numbers one per line, and the stored switch", () => {
    render(
      <ChannelForm channel={sms("+31612345678, +31687654321")} onSave={async () => {}} />,
    );
    const numbers = screen.getByLabelText("Phone numbers") as HTMLTextAreaElement;
    expect(numbers.tagName).toBe("TEXTAREA");
    expect(numbers.value).toBe("+31612345678\n+31687654321");
    const back = screen.getByRole("checkbox", { name: /back up/i }) as HTMLInputElement;
    expect(back.checked).toBe(false);
  });

  it("sends masked numbers back unchanged, and never offers them for editing", async () => {
    // An editor is sent the masked list. Put in a text box, it would be
    // saved as literal bullets; echoed back, the API reads it as unchanged.
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={sms(MASKED)} onSave={onSave} />);
    for (const box of screen.queryAllByRole("textbox")) {
      expect((box as HTMLInputElement).value).not.toContain("\u2022");
    }
    expect(screen.getByText(/only an administrator reads these/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].config).toEqual({
      provider: "android-gateway",
      numbers: MASKED,
      url: "****8080",
      username: "sms",
      password: "****word",
      recoveries: "false",
    });
  });

  it("replaces masked numbers only as a whole new list", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ChannelForm channel={sms(MASKED)} onSave={onSave} />);
    fireEvent.click(screen.getByRole("button", { name: /replace phone numbers/i }));
    const numbers = screen.getByLabelText("Phone numbers") as HTMLTextAreaElement;
    expect(numbers.value).toBe("");
    fireEvent.change(numbers, { target: { value: "+31611112222" } });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    expect(onSave.mock.calls[0][0].config.numbers).toBe("+31611112222");
  });

  it("warns about quiet hours on an SMS channel only", () => {
    const { unmount } = render(<ChannelForm channel={sms("+31612345678")} onSave={async () => {}} />);
    const box = screen.getByRole("checkbox", { name: /hold this channel/i });
    expect(box.getAttribute("aria-describedby")).toBeTruthy();
    const help = document.getElementById(box.getAttribute("aria-describedby") ?? "");
    expect(help?.textContent).toMatch(/defeats the reason for choosing SMS/);
    unmount();
    render(<ChannelForm channel={slack()} onSave={async () => {}} />);
    expect(screen.queryByText(/defeats the reason/)).toBeNull();
  });
});
