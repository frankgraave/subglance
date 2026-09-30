/**
 * The notification-channel model: a delivery destination as the page reads it.
 *
 * **Everything here is derived from what `internal/api/channels.go` actually
 * returns.** That file masks every config value that is not on its
 * `publicKeys` allowlist, so a stored webhook URL or bot token comes back as
 * `****` plus its last four characters and nothing else. The interface is
 * built around that fact rather than around the mockup, which drew a full
 * destination string for a Slack row: a page that renders a destination it
 * never received would have to invent one.
 *
 * **Delivery history is modelled as absent, not as healthy.** The mockup puts
 * "Delivered / Failed ×11 / Never fired" on every row, and the ticket asks for
 * three distinguishable states. The API offers none of them: `GET /channels`
 * carries no delivery counts, no last error and no last-sent timestamp, and
 * the one place that knowledge exists — `store.ChannelHealthSince` — is not
 * wired to any handler. So the third state on this page is *unknown*, and it
 * says so in those words. Painting a green tick on a channel whose last eleven
 * deliveries failed is the single most dangerous thing this screen could do,
 * and it is exactly what "assume healthy until told otherwise" produces.
 */

/** The eight types `store` accepts, mirroring its CHECK constraint. */
export type ChannelType =
  | "webhook"
  | "discord"
  | "slack"
  | "telegram"
  | "email"
  | "ntfy"
  | "gotify"
  | "sms";

export const CHANNEL_TYPES: readonly ChannelType[] = [
  "email",
  "slack",
  "discord",
  "telegram",
  "ntfy",
  "gotify",
  "sms",
  "webhook",
];

/**
 * A channel's daily quiet window, as `store.QuietHours` writes it.
 *
 * `during` is kept as a string for the same reason `type` is: a server newer
 * than this build may know a third mode, and narrowing it would make the row
 * describe a behaviour the channel does not have.
 */
export type ApiQuietHours = {
  start: string;
  end: string;
  timezone: string;
  during: string;
};

export type QuietHours = {
  start: string;
  end: string;
  timezone: string;
  during: string;
};

/** The wire shape of one channel, as `channelResponse` writes it. */
export type ApiChannel = {
  id: number | string;
  name: string;
  type: string;
  config?: Record<string, string> | null;
  enabled?: boolean;
  is_default?: boolean;
  quiet_hours?: ApiQuietHours | null;
  created_at?: string;
  updated_at?: string;
};

export type Channel = {
  id: string;
  name: string;
  /**
   * The wire type, kept verbatim rather than narrowed to `ChannelType`. A
   * server newer than this build can name a type this one has no field set
   * for, and coercing it would make the row claim to be something else.
   */
  type: string;
  /** Config exactly as the API returned it — secrets already masked there. */
  config: Readonly<Record<string, string>>;
  enabled: boolean;
  /**
   * The instance default: where a monitor with no channels of its own sends
   * its alerts. Absent on the wire reads as false — a server that predates the
   * default has none, and claiming one would promise alerts nobody sends.
   */
  isDefault: boolean;
  /**
   * The daily quiet window, or null when there is none. A server that
   * predates quiet hours sends no field, which reads as none: that server
   * holds nothing back.
   */
  quietHours: QuietHours | null;
  createdAt: number | null;
};

export function channelFromApi(api: ApiChannel): Channel {
  return {
    id: String(api.id),
    name: api.name,
    type: api.type,
    config: api.config ?? {},
    enabled: api.enabled !== false,
    isDefault: api.is_default === true,
    quietHours: quietFromApi(api.quiet_hours),
    createdAt: toUnixMs(api.created_at),
  };
}

export function channelsFromPayload(
  payload: { channels?: ApiChannel[] } | null | undefined,
): Channel[] {
  return (payload?.channels ?? []).map(channelFromApi);
}

function quietFromApi(
  api: ApiQuietHours | null | undefined,
): QuietHours | null {
  if (api === null || api === undefined || typeof api !== "object") {
    return null;
  }
  return {
    start: api.start ?? "",
    end: api.end ?? "",
    timezone: api.timezone ?? "",
    during: api.during ?? "",
  };
}

/**
 * One line saying when a channel stays silent and what happens meanwhile.
 *
 * The mode is always in the sentence, never implied. "Quiet 23:00–07:00" on
 * its own reads as "held", which is the safe reading, and a channel set to
 * drop would then be discarding alerts behind a line that suggested the
 * opposite. A mode this build does not know is named as unknown rather than
 * guessed at.
 */
export function describeQuietHours(quiet: QuietHours): string {
  const window = `quiet ${quiet.start}–${quiet.end} ${quiet.timezone}`;
  switch (quiet.during) {
    case "hold":
      return `${window}, alerts held for one digest`;
    case "drop":
      return `${window}, alerts dropped`;
    default:
      return `${window}, unknown handling "${quiet.during}"`;
  }
}

/**
 * The same fact in chip length: the window and one word for the mode.
 *
 * The word stays even though the chip is short. Hold is the answer that cannot
 * lose an alert, and a chip reading only "Quiet 23:00–07:00" on a channel that
 * drops would let the dangerous setting pass for the safe one.
 */
export function quietChip(quiet: QuietHours): string {
  const mode =
    quiet.during === "hold"
      ? "held"
      : quiet.during === "drop"
        ? "dropped"
        : "unknown";
  return `Quiet ${quiet.start}–${quiet.end}, ${mode}`;
}

function toUnixMs(value: string | undefined): number | null {
  if (value === undefined || value === "") return null;
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : null;
}

/** How a type is written in the interface. Unknown stays honest. */
export function typeLabel(type: string): string {
  switch (type) {
    case "email":
      return "Email";
    case "slack":
      return "Slack";
    case "discord":
      return "Discord";
    case "telegram":
      return "Telegram";
    case "ntfy":
      return "ntfy";
    case "gotify":
      return "Gotify";
    case "sms":
      return "SMS";
    case "webhook":
      return "Webhook";
    default:
      return "Unknown type";
  }
}

export function isKnownType(type: string): type is ChannelType {
  return (CHANNEL_TYPES as readonly string[]).includes(type);
}

/**
 * One editable setting on a channel.
 *
 * `secret` is not a styling hint. It decides three separate things: that the
 * stored value is never rendered, that the control which accepts a new one is
 * `type="password"`, and that an untouched field is sent back unchanged rather
 * than overwritten.
 */
export type FieldSpec = {
  key: string;
  label: string;
  secret: boolean;
  required: boolean;
  help?: string;
  placeholder?: string;
  /**
   * The control that takes the value. Text by default. Every kind still
   * stores a string, because that is all a channel's config holds: a
   * checkbox writes "true" or "false", a list writes one entry per line.
   */
  control?: "select" | "checkbox" | "list";
  /** The choices of a select; the first is the default. */
  options?: readonly { value: string; label: string }[];
  /**
   * Shown, validated and sent only while another field holds this value. A
   * hidden field is not sent at all, so switching an SMS channel from one
   * provider to the other drops the first provider's credentials instead of
   * storing them where nothing reads them.
   */
  when?: { key: string; value: string };
  /**
   * The API may send this value back masked for readers who are not
   * administrators (an SMS channel's phone numbers). Masked, it is treated
   * like a stored secret: shown as it arrived, sent back unchanged, and only
   * replaceable as a whole.
   */
  personal?: boolean;
};

/**
 * The fields each type needs, taken from the senders in `internal/notifier/`
 * and the validation in `internal/api/channels.go` — not from the mockup.
 *
 * The mockup draws a Slack "channel label", a webhook HTTP method and a
 * webhook signing secret. None of the three exist: `SlackSender` reads only
 * `url`, `WebhookSender` reads `url` and `headers` and always POSTs, and there
 * is no signing anywhere in the notifier. Offering them would be a form that
 * saves settings nothing reads.
 */
export const FIELDS: Readonly<Record<ChannelType, readonly FieldSpec[]>> = {
  email: [
    {
      key: "to",
      label: "Recipient address",
      secret: false,
      required: true,
      help: "Several addresses may be separated by commas.",
      placeholder: "ops@example.com",
    },
    { key: "from", label: "From address", secret: false, required: false },
    { key: "host", label: "SMTP host", secret: false, required: false },
    {
      key: "port",
      label: "SMTP port",
      secret: false,
      required: false,
      placeholder: "587",
    },
    { key: "username", label: "SMTP username", secret: false, required: false },
    {
      key: "password",
      label: "SMTP password",
      secret: true,
      required: false,
      help: "Stored write-only. It is never sent back to this page.",
    },
  ],
  slack: [
    {
      key: "url",
      label: "Incoming webhook URL",
      secret: true,
      required: true,
      help: "The URL is the credential, so it is stored write-only and never shown again.",
      placeholder: "https://hooks.slack.com/services/…",
    },
  ],
  discord: [
    {
      key: "url",
      label: "Webhook URL",
      secret: true,
      required: true,
      help: "The URL is the credential, so it is stored write-only and never shown again.",
      placeholder: "https://discord.com/api/webhooks/…",
    },
  ],
  telegram: [
    {
      key: "bot_token",
      label: "Bot token",
      secret: true,
      required: true,
      help: "Stored write-only. It is never sent back to this page.",
      placeholder: "123456:ABC-DEF…",
    },
    {
      key: "chat_id",
      label: "Chat ID",
      secret: false,
      required: true,
      help: "Not a secret: it names a destination but grants nothing, so it is shown in full.",
    },
  ],
  ntfy: [
    {
      key: "topic",
      label: "Topic",
      secret: true,
      required: true,
      help: "Letters, digits, - and _. On a server without login the topic is the password, so it is stored write-only.",
    },
    {
      key: "url",
      label: "Server URL",
      secret: true,
      required: false,
      help: "Leave empty for ntfy.sh. A server on your own network needs --allow-private-targets.",
      placeholder: "https://ntfy.sh",
    },
    {
      key: "token",
      label: "Access token",
      secret: true,
      required: false,
      help: "Or a username and password below, not both.",
      placeholder: "tk_…",
    },
    { key: "username", label: "Username", secret: false, required: false },
    { key: "password", label: "Password", secret: true, required: false },
  ],
  gotify: [
    {
      key: "url",
      label: "Server URL",
      secret: true,
      required: true,
      help: "Include any sub-path. A server on your own network needs --allow-private-targets.",
      placeholder: "https://gotify.example.com",
    },
    {
      key: "token",
      label: "Application token",
      secret: true,
      required: true,
      help: "From the Apps page in Gotify. Stored write-only.",
    },
    {
      key: "priority_down",
      label: "Priority when down",
      secret: false,
      required: false,
      help: "0 to 10. Gotify's app plays a sound from 4 and pops up from 8.",
      placeholder: "8",
    },
    {
      key: "priority_up",
      label: "Priority when back up",
      secret: false,
      required: false,
      placeholder: "4",
    },
  ],
  /*
   * One type with a provider choice, as `notifier.SMSSender` reads it. The
   * gateway URL, its password and the Twilio auth token are masked by the API;
   * the account SID names an account like a username does and is public.
   */
  sms: [
    {
      key: "provider",
      label: "Sent through",
      secret: false,
      required: true,
      control: "select",
      options: [
        { value: "android-gateway", label: "SMS Gateway for Android" },
        { value: "twilio", label: "Twilio" },
      ],
      help: "SMS Gateway for Android turns a phone with a SIM card into the sender: no account, no charge per message. Twilio bills every message.",
    },
    {
      key: "numbers",
      label: "Phone numbers",
      secret: false,
      required: true,
      control: "list",
      personal: true,
      help: "One per line, up to 10. A number without + or 00 in front uses the country code below.",
      placeholder: "+31 6 1234 5678",
    },
    {
      key: "country_code",
      label: "Country code for numbers without +",
      secret: false,
      required: false,
      help: "With +31 here, 06 1234 5678 is read as a Dutch mobile number.",
      placeholder: "+31",
    },
    {
      key: "url",
      label: "Gateway address",
      secret: true,
      required: true,
      when: { key: "provider", value: "android-gateway" },
      help: "The address the app shows under Local Server. A phone on your own network needs --allow-private-targets. Stored write-only.",
      placeholder: "http://192.168.1.50:8080",
    },
    {
      key: "username",
      label: "Gateway username",
      secret: false,
      required: true,
      when: { key: "provider", value: "android-gateway" },
    },
    {
      key: "password",
      label: "Gateway password",
      secret: true,
      required: true,
      when: { key: "provider", value: "android-gateway" },
      help: "Stored write-only. It is never sent back to this page.",
    },
    {
      key: "account_sid",
      label: "Account SID",
      secret: false,
      required: true,
      when: { key: "provider", value: "twilio" },
      placeholder: "AC…",
    },
    {
      key: "auth_token",
      label: "Auth token",
      secret: true,
      required: true,
      when: { key: "provider", value: "twilio" },
      help: "Stored write-only. It is never sent back to this page.",
    },
    {
      key: "from",
      label: "Sender",
      secret: false,
      required: true,
      when: { key: "provider", value: "twilio" },
      help: "Your Twilio number, or a name of up to 11 letters and digits. Not every country accepts a name; Twilio's refusal is shown if yours does not.",
      placeholder: "+14155550100",
    },
    {
      key: "hourly_limit",
      label: "Messages per hour, at most",
      secret: false,
      required: false,
      help: "1 to 100, 10 when empty. Alerts over the limit are not sent by SMS; the next message says how many were held back. A test message counts toward it.",
      placeholder: "10",
    },
    {
      key: "recoveries",
      label: "Also send a message when a monitor is back up",
      secret: false,
      required: false,
      control: "checkbox",
      help: "Off means outages only: one message per incident instead of two.",
    },
    {
      key: "timezone",
      label: "Time zone for times in a message",
      secret: false,
      required: false,
      help: "Empty uses the server's zone and names it after each time.",
      placeholder: "Europe/Amsterdam",
    },
  ],
  webhook: [
    {
      key: "url",
      label: "Endpoint URL",
      secret: true,
      required: true,
      help: "Treated as a credential: the API masks it on read, so it cannot be shown back to you.",
      placeholder: "https://example.com/hooks/subglance",
    },
    {
      key: "headers",
      label: "Extra headers",
      secret: true,
      required: false,
      help: "One Name: value per line. Masked on read because a header is where an API key goes.",
    },
  ],
};

export function fieldsFor(type: string): readonly FieldSpec[] {
  return isKnownType(type) ? FIELDS[type] : [];
}

/**
 * The fields of a type that apply with these values: a field tied to another
 * field's value (`when`) is left out unless that value is chosen. A select
 * with nothing chosen yet counts as holding its first option, which is what
 * it shows.
 */
export function visibleFields(
  type: string,
  values: Readonly<Record<string, string>>,
): readonly FieldSpec[] {
  const specs = fieldsFor(type);
  return specs.filter((spec) => {
    if (spec.when === undefined) return true;
    const { key, value } = spec.when;
    return fieldValue(specs, values, key) === value;
  });
}

/** A field's value, or a select's first option when none is set. */
export function fieldValue(
  specs: readonly FieldSpec[],
  values: Readonly<Record<string, string>>,
  key: string,
): string {
  const value = values[key];
  if (value !== undefined && value !== "") return value;
  const spec = specs.find((candidate) => candidate.key === key);
  return spec?.control === "select" ? (spec.options?.[0]?.value ?? "") : "";
}

/** Splits a list setting the way the server does: commas, semicolons, lines. */
export function listEntries(value: string): string[] {
  return value
    .split(/[,;\r\n]+/)
    .map((entry) => entry.trim())
    .filter((entry) => entry !== "");
}

/**
 * Whether a phone-number list arrived in the API's masked form.
 *
 * `notifier.MaskSMSNumbers` writes each number as `+31 6 •••• 5678`, and a
 * part it cannot read as `••••`. No real number contains a bullet, so one
 * bullet is enough to know this reader was not shown the numbers.
 */
export function isMaskedList(value: string): boolean {
  return value.includes("\u2022");
}

/**
 * Whether a value the API sent back is a mask rather than a real value.
 *
 * `maskValue` in the Go handler produces `****` plus the last four characters,
 * or a run of asterisks for anything four characters or shorter. Recognising
 * that shape is what lets the form say "a secret is stored" without ever
 * needing the secret — and what lets it send the mask straight back, which the
 * handler reads as "unchanged".
 */
export function isMasked(value: string): boolean {
  if (value === "") return false;
  if (/^\*+$/.test(value)) return true;
  return /^\*{4}.{4}$/.test(value);
}

/** Whether a secret is stored for this field at all. */
export function hasSecret(channel: Channel, key: string): boolean {
  const value = channel.config[key];
  return value !== undefined && value !== "";
}

/**
 * The line under a channel's name: where it delivers, as far as we may know.
 *
 * For email and Telegram the destination is public config and is printed in
 * full. For the three webhook types it is the credential itself, so the only
 * honest thing to print is the mask the API returned — enough to tell two
 * Slack webhooks apart, which is the whole reason the Go handler keeps the
 * tail rather than blanking the value.
 */
export function describeDestination(channel: Channel): string {
  const cfg = channel.config;
  switch (channel.type) {
    case "email": {
      const to = cfg.to ?? "";
      return to === "" ? "no recipient configured" : to;
    }
    case "telegram": {
      const chat = cfg.chat_id ?? "";
      return chat === "" ? "no chat configured" : `chat ${chat}`;
    }
    case "ntfy": {
      /*
       * The topic is masked by the API (it is the credential on an open
       * server), and an empty server URL means the public default.
       */
      const topic = cfg.topic ?? "";
      if (topic === "") return "no topic configured";
      const server = (cfg.url ?? "") === "" ? "ntfy.sh" : "own server";
      return `${server}, topic ending ${topic}`;
    }
    case "sms": {
      /*
       * A count and a provider, never the numbers. An administrator's API
       * answer carries them in full, and this line sits in a list that is
       * shown on screen shares; which phones a channel rings is in its form.
       */
      const count = listEntries(cfg.numbers ?? "").length;
      if (count === 0) return "no phone numbers configured";
      const via =
        cfg.provider === "twilio"
          ? "Twilio"
          : cfg.provider === "android-gateway"
            ? "SMS Gateway for Android"
            : "an unknown provider";
      return `${count} phone ${count === 1 ? "number" : "numbers"} through ${via}`;
    }
    case "slack":
    case "discord":
    case "gotify":
    case "webhook": {
      const url = cfg.url ?? "";
      if (url === "") return "no endpoint configured";
      return isMasked(url) ? `endpoint ending ${url}` : url;
    }
    default:
      return "this build does not know this channel type";
  }
}

/**
 * What one "Send test" attempt reported, or that none has been made.
 *
 * Deliberately *not* a delivery history. It describes a test this browser ran,
 * nothing more, and `unknown` is the state every channel is in when the page
 * loads — including one that has been failing for three days.
 */
export type DeliveryState =
  | { kind: "unknown" }
  | { kind: "passed" }
  | { kind: "failed"; error: string };

export const DELIVERY_UNKNOWN: DeliveryState = { kind: "unknown" };

/**
 * The words the delivery cell says.
 *
 * A string rather than JSX, because the wording *is* the decision here and it
 * deserves a test that needs no renderer. "Not verified" is the load-bearing
 * one: it is not "ok", it is not "failed", and it must never read as either.
 */
export function describeDelivery(state: DeliveryState): string {
  switch (state.kind) {
    case "passed":
      return "Test delivered";
    case "failed":
      return "Test failed";
    default:
      return "Not verified";
  }
}
