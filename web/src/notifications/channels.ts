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

/** The five types `store` accepts, mirroring its CHECK constraint. */
export type ChannelType =
  | "webhook"
  | "discord"
  | "slack"
  | "telegram"
  | "email";

export const CHANNEL_TYPES: readonly ChannelType[] = [
  "email",
  "slack",
  "discord",
  "telegram",
  "webhook",
];

/** The wire shape of one channel, as `channelResponse` writes it. */
export type ApiChannel = {
  id: number | string;
  name: string;
  type: string;
  config?: Record<string, string> | null;
  enabled?: boolean;
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
  createdAt: number | null;
};

export function channelFromApi(api: ApiChannel): Channel {
  return {
    id: String(api.id),
    name: api.name,
    type: api.type,
    config: api.config ?? {},
    enabled: api.enabled !== false,
    createdAt: toUnixMs(api.created_at),
  };
}

export function channelsFromPayload(
  payload: { channels?: ApiChannel[] } | null | undefined,
): Channel[] {
  return (payload?.channels ?? []).map(channelFromApi);
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
    case "slack":
    case "discord":
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

/** The heading count: "4 channels, 1 disabled". */
export function describeChannels(channels: readonly Channel[]): string {
  if (channels.length === 0) return "No channels configured";
  const disabled = channels.filter((c) => !c.enabled).length;
  const total = `${channels.length} channel${channels.length === 1 ? "" : "s"}`;
  return disabled === 0 ? total : `${total}, ${disabled} disabled`;
}
