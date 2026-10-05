import { describe, expect, it } from "vitest";
import {
  channelFromApi,
  channelsFromPayload,
  describeDelivery,
  describeDestination,
  describeHistory,
  historyChip,
  historyFromApi,
  describeQuietHours,
  fieldsFor,
  hasSecret,
  isMasked,
  isMaskedList,
  listEntries,
  quietChip,
  typeLabel,
  visibleFields,
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

describe("quiet hours", () => {
  const night = {
    start: "23:00",
    end: "07:00",
    timezone: "Europe/Amsterdam",
    during: "hold",
  };

  it("reads a channel without the field as having no window", () => {
    // A server from before quiet hours holds nothing back.
    expect(make().quietHours).toBeNull();
    expect(make({ quiet_hours: null }).quietHours).toBeNull();
    expect(make({ quiet_hours: night }).quietHours).toEqual(night);
  });

  it("always names the mode, so drop never passes for hold", () => {
    expect(describeQuietHours(night)).toBe(
      "quiet 23:00–07:00 Europe/Amsterdam, alerts held for one digest",
    );
    expect(describeQuietHours({ ...night, during: "drop" })).toMatch(/dropped$/);
    expect(quietChip(night)).toBe("Quiet 23:00–07:00, held");
    expect(quietChip({ ...night, during: "drop" })).toBe(
      "Quiet 23:00–07:00, dropped",
    );
  });

  it("does not guess at a mode it does not know", () => {
    const later = { ...night, during: "page" };
    expect(describeQuietHours(later)).toMatch(/unknown handling "page"/);
    expect(quietChip(later)).toMatch(/unknown$/);
  });
});

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

  it("names an ntfy topic only by its masked tail, and which server", () => {
    // The API masks the topic: on an open ntfy server it is the credential.
    expect(
      describeDestination(
        make({ type: "ntfy", config: { topic: "****erts" } }),
      ),
    ).toBe("ntfy.sh, topic ending ****erts");
    expect(
      describeDestination(
        make({
          type: "ntfy",
          config: { topic: "****erts", url: "****5:80" },
        }),
      ),
    ).toBe("own server, topic ending ****erts");
    expect(
      describeDestination(
        make({ type: "gotify", config: { url: "****.lan" } }),
      ),
    ).toBe("endpoint ending ****.lan");
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
    // No test has run from this browser, so "healthy" is a claim nothing
    // behind this word supports. The channel's real history is a separate
    // record (historyFromApi), and says its own thing.
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
    // The mockup draws a Slack channel label and a webhook signing secret.
    // SlackSender reads `url` and nothing else; nothing signs anything.
    // WebhookSender reads `url`, `headers`, `method` and `body`.
    expect(fieldsFor("slack").map((f) => f.key)).toEqual(["url"]);
    expect(fieldsFor("webhook").map((f) => f.key)).toEqual([
      "url",
      "headers",
      "method",
      "body",
    ]);
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
    expect(secretKeys("ntfy")).toEqual(["topic", "url", "token", "password"]);
    expect(secretKeys("gotify")).toEqual(["url", "token"]);
    // The gateway URL, its password and the Twilio auth token; the account
    // SID is on the allowlist, and the numbers have a mask of their own.
    expect(secretKeys("sms")).toEqual(["url", "password", "auth_token"]);
    // chat_id, to, from, host, port and username are on the allowlist.
    expect(
      fieldsFor("email")
        .filter((f) => !f.secret)
        .map((f) => f.key),
    ).toEqual(["to", "from", "host", "port", "username"]);
  });

  it("offers an SMS provider's fields only while that provider is chosen", () => {
    // Hidden fields are not sent, so a channel switched to Twilio does not
    // keep a gateway password nothing reads.
    const keys = (values: Record<string, string>) =>
      visibleFields("sms", values).map((f) => f.key);
    const shared = ["provider", "numbers", "country_code"];
    const tail = ["hourly_limit", "recoveries", "timezone"];
    // Nothing chosen reads as the first option, which is what the select shows.
    expect(keys({})).toEqual([...shared, "url", "username", "password", ...tail]);
    expect(keys({ provider: "twilio" })).toEqual([
      ...shared,
      "account_sid",
      "auth_token",
      "from",
      ...tail,
    ]);
    // A provider this build does not know gets no credentials to fill in.
    expect(keys({ provider: "vonage" })).toEqual([...shared, ...tail]);
  });

  it("returns nothing for a type it has no field set for", () => {
    expect(fieldsFor("pagerduty")).toEqual([]);
  });
});

describe("SMS numbers", () => {
  it("splits a list the way the server does", () => {
    // notifier.smsRecipients splits on commas, semicolons and line breaks.
    expect(listEntries("+31612345678, 06 1234 5678;\r\n\n+44 7700 900123 ")).toEqual([
      "+31612345678",
      "06 1234 5678",
      "+44 7700 900123",
    ]);
    expect(listEntries(" \n ")).toEqual([]);
  });

  it("recognises the masked form an editor is sent", () => {
    expect(isMaskedList("+31 6 \u2022\u2022\u2022\u2022 5678, \u2022\u2022\u2022\u2022")).toBe(true);
    expect(isMaskedList("+31612345678")).toBe(false);
  });

  it("describes an SMS channel by count and provider, never by number", () => {
    // An administrator's answer carries the numbers in full; this line is in
    // a list that gets screen-shared.
    const text = describeDestination(
      make({
        type: "sms",
        config: { provider: "twilio", numbers: "+31612345678, +31687654321" },
      }),
    );
    expect(text).toBe("2 phone numbers through Twilio");
    expect(text).not.toMatch(/\d{4}/);
    expect(
      describeDestination(
        make({ type: "sms", config: { provider: "android-gateway", numbers: "+31612345678" } }),
      ),
    ).toBe("1 phone number through SMS Gateway for Android");
    expect(describeDestination(make({ type: "sms", config: {} }))).toBe(
      "no phone numbers configured",
    );
    expect(typeLabel("sms")).toBe("SMS");
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

describe("historyFromApi (SUB-180)", () => {
  it("reads a missing or unrecognised record as unknown, never healthy", () => {
    // An older server sends no record; a newer one may name a state this
    // build has no word for. Either way the page has been told nothing.
    expect(historyFromApi(undefined).state).toBe("unknown");
    expect(historyFromApi(null).state).toBe("unknown");
    expect(historyFromApi({ state: "degraded" }).state).toBe("unknown");
    expect(historyChip(historyFromApi(undefined))).toBeNull();
    expect(describeHistory(historyFromApi(undefined))).toMatch(/Not verified/);
  });

  it("keeps the counts, the moments and the error the server sent", () => {
    const h = historyFromApi({
      state: "failed",
      window_days: 30,
      last_failed_at: "2026-09-30T09:00:00Z",
      last_delivered_at: null,
      failed: 1,
      pending: 0,
      retrying: 0,
      last_error: "no such host",
    });
    expect(h.state).toBe("failed");
    expect(h.lastFailedAt).toBe(Date.parse("2026-09-30T09:00:00Z"));
    expect(h.lastDeliveredAt).toBeNull();
    expect(describeHistory(h)).toMatch(/^1 alert gave up in the last 30 days, the newest .*: no such host$/);
  });

  it("gives each state its own word and never shares a colour", () => {
    const words = ["delivered", "failed", "retrying", "none"].map((state) =>
      historyChip(historyFromApi({ state, window_days: 30 })),
    );
    expect(words.map((w) => w?.word)).toEqual([
      "Delivered",
      "Failed",
      "Retrying",
      "None in 30 days",
    ]);
    expect(words.map((w) => w?.status)).toEqual(["up", "down", "warn", null]);
  });
});
