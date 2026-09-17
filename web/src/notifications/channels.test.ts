import { describe, expect, it } from "vitest";
import {
  channelFromApi,
  channelsFromPayload,
  describeDelivery,
  describeDestination,
  fieldsFor,
  hasSecret,
  isMasked,
  typeLabel,
} from "./channels";

/*
 * The channel model.
 *
 * Every assertion here is about a claim the page makes on the strength of this
 * file: whether a masked value is recognised as one, whether an unverified
 * channel can be mistaken for a healthy one, and whether a form offers a field
 * the notifier never reads.
 */

function make(over: Record<string, unknown> = {}) {
  return channelFromApi({
    id: 1,
    name: "On-call Slack",
    type: "slack",
    config: { url: "****B07F" },
    enabled: true,
    created_at: "2026-09-01T10:00:00Z",
    ...over,
  });
}

describe("isMasked", () => {
  it("recognises the shape maskValue produces", () => {
    // `****` plus the last four characters, which is what the Go handler
    // returns for anything longer than four — and echoing it back unchanged is
    // the only spelling of "leave this credential alone" that PUT accepts.
    expect(isMasked("****B07F")).toBe(true);
    expect(isMasked("****")).toBe(true);
    expect(isMasked("**")).toBe(true);
  });

  it("does not call a real value masked", () => {
    expect(isMasked("https://hooks.slack.com/services/T/B/x")).toBe(false);
    expect(isMasked("")).toBe(false);
  });
});

describe("describeDestination", () => {
  it("prints a public destination in full", () => {
    // `to` and `chat_id` are on the API's publicKeys allowlist, so they come
    // back unmasked and there is nothing to hide.
    expect(
      describeDestination(
        make({ type: "email", config: { to: "ops@example.com" } }),
      ),
    ).toBe("ops@example.com");
    expect(
      describeDestination(
        make({ type: "telegram", config: { chat_id: "-100123" } }),
      ),
    ).toBe("chat -100123");
  });

  it("never invents a destination for a masked webhook URL", () => {
    // The URL *is* the credential for Slack, Discord and webhook, so the API
    // masks it. Printing a plausible-looking endpoint would be fiction.
    const text = describeDestination(make());
    expect(text).toBe("endpoint ending ****B07F");
    expect(text).not.toContain("hooks.slack.com");
  });

  it("says so when a channel type this build does not know arrives", () => {
    expect(describeDestination(make({ type: "pagerduty" }))).toMatch(
      /does not know/,
    );
    expect(typeLabel("pagerduty")).toBe("Unknown type");
  });
});

describe("describeDelivery", () => {
  it("calls an untested channel not verified, never ok", () => {
    // The API carries no delivery history at all, so "healthy" is a claim
    // nothing behind this page supports. A channel that has failed every
    // delivery for three days is in exactly this state.
    expect(describeDelivery({ kind: "unknown" })).toBe("Not verified");
    expect(describeDelivery({ kind: "unknown" })).not.toMatch(/deliver|ok/i);
  });

  it("distinguishes a passed test from a failed one in words", () => {
    expect(describeDelivery({ kind: "passed" })).toBe("Test delivered");
    expect(describeDelivery({ kind: "failed", error: "401" })).toBe(
      "Test failed",
    );
  });
});

describe("fieldsFor", () => {
  it("offers only the settings the senders actually read", () => {
    // The mockup draws a Slack channel label, an HTTP method and a webhook
    // signing secret. SlackSender reads `url` and nothing else; WebhookSender
    // reads `url` and `headers` and always POSTs; nothing signs anything.
    expect(fieldsFor("slack").map((f) => f.key)).toEqual(["url"]);
    expect(fieldsFor("webhook").map((f) => f.key)).toEqual(["url", "headers"]);
    expect(fieldsFor("telegram").map((f) => f.key)).toEqual([
      "bot_token",
      "chat_id",
    ]);
  });

  it("marks exactly the fields the API masks as secret", () => {
    // Deny by default on the server: anything not on `publicKeys` comes back
    // masked. A field this file called public that the API masks would show a
    // row of asterisks in a text box and save them as a literal.
    const secretKeys = (type: string) =>
      fieldsFor(type)
        .filter((f) => f.secret)
        .map((f) => f.key);
    expect(secretKeys("telegram")).toEqual(["bot_token"]);
    expect(secretKeys("email")).toEqual(["password"]);
    expect(secretKeys("webhook")).toEqual(["url", "headers"]);
    // chat_id, to, from, host, port and username are on the allowlist.
    expect(
      fieldsFor("email")
        .filter((f) => !f.secret)
        .map((f) => f.key),
    ).toEqual(["to", "from", "host", "port", "username"]);
  });

  it("returns nothing for a type it has no field set for", () => {
    expect(fieldsFor("pagerduty")).toEqual([]);
  });
});

describe("hasSecret", () => {
  it("reports a stored credential from the mask alone", () => {
    expect(hasSecret(make(), "url")).toBe(true);
    expect(hasSecret(make({ config: {} }), "url")).toBe(false);
    expect(hasSecret(make({ config: { url: "" } }), "url")).toBe(false);
  });
});

describe("channelsFromPayload", () => {
  it("reads ids as strings, because the rest of the app keys on them", () => {
    const [channel] = channelsFromPayload({ channels: [{ id: 7, name: "a", type: "email" }] });
    expect(channel.id).toBe("7");
  });

  it("treats a missing enabled flag as enabled, matching the server default", () => {
    const [channel] = channelsFromPayload({
      channels: [{ id: 1, name: "a", type: "email" }],
    });
    expect(channel.enabled).toBe(true);
  });
});
