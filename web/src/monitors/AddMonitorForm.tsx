import { useEffect, useId, useRef, useState } from "react";
import type { FormEvent } from "react";
import { RepeatAlertField } from "./RepeatAlertField";
import { AlertingSection } from "./AlertingSection";
import { DurationField } from "./DurationField";
import { durationAllowed, DURATION_LIMITS, SECONDS_ONLY, SECONDS_TO_DAYS, SECONDS_TO_HOURS } from "./duration";
import type { DurationUnit } from "./duration";
import { ChannelPicker } from "./ChannelPicker";
import { channelIdsFromText, channelIdsText } from "./channelChoice";
import type { Channel } from "../notifications/channels";
import { validRepeat, REPEAT_ERROR } from "./repeat";
import { isPush } from "./push";
import { TlsFloorField } from "./TlsFloorField";
import { TLS_FLOOR_UNSET } from "./tlsFloor";
import type { PreviewResult, PreviewState } from "./preview";
import { describePreview, suggestName } from "./preview";
import { JSON_HELP, JSON_OPERATORS } from "./jsonAssertion";
import { DNS_EMPTY_HELP, DNS_EXPECTED_HELP, DNS_EXPECTED_PLACEHOLDER, DNS_RECORD_TYPES, DNS_RESOLVER_HELP } from "./dnsCheck";
import { Select } from "../components/Select";
import { FieldError } from "../components/FieldError";

/**
 * Add a monitor in under sixty seconds (DESIGN.md §7.2, product principle 2).
 *
 * Three decisions carry this screen.
 *
 * **One required field.** Paste an address; everything else has a default the
 * server already applies. Type is not asked for — it is inferred from what was
 * pasted, and the preview tells the user what the inference decided, so the
 * dropdown that would otherwise be the second field disappears. Name is
 * suggested from the host and stays editable.
 *
 * **Test before save, not after.** Saving first and finding out later is the
 * shape that fails the sixty seconds: a missing scheme costs a save, a wait for
 * the first scheduled check, and an edit. The preview probes the real target
 * from the real server and writes nothing.
 *
 * **The advanced options are collapsed, not absent.** A `<details>` rather
 * than a second screen, because the person who needs a keyword check needs it
 * on the first monitor, not after discovering a settings page.
 *
 * Presentational: it owns its field values, and the caller owns the network.
 * That is what lets a test drive every preview phase without a fetch.
 */

export type AddMonitorValues = {
  name: string;
  target: string;
  /** Empty means "let the server infer it". */
  type: string;
  intervalS: number;
  timeoutS: number;
  keyword: string;
  keywordMode: string;
  /** Push monitors only: how often the job is expected to report, in seconds. */
  pushIntervalS: number;
  /** Push monitors only: how late that report may be, in seconds. */
  pushGraceS: number;
  repeatAfterS: number;
  /** Passing checks in a row that close a confirmed incident. Not for push. */
  recoveryThreshold: number;
  /**
   * The lowest TLS version this monitor may negotiate, written "1.0" to
   * "1.3" — or `""` for no opinion, which is the default and is NOT the same
   * as "1.2". The empty value must reach the caller as an omitted field.
   */
  minTlsVersion: string;
  /** JSON body assertion; an empty path means none. See jsonAssertion.ts. */
  jsonPath: string;
  jsonOperator: string;
  jsonExpected: string;
  /** DNS monitors only: the record to ask for, the values one per line, and the resolver. */
  dnsRecordType: string;
  dnsExpected: string;
  dnsResolver: string;
  /** Domain monitors only: days of warning before the registration expires. */
  domainWarnDays: number;
  /**
   * The monitor's own channels, as sorted comma-joined ids: text rather than
   * an array so ticking a box and unticking it again reads as no change.
   */
  channelIds: string;
};

/**
 * A rejection the form has to show, and where it belongs.
 *
 * `field` is the JSON name the server blamed. It is deliberately not an enum
 * of the inputs this form happens to render: the API may name a field the
 * form has no control for (`ssl_warn_days`, say, which only the edit screen
 * will offer), and the honest answer there is to show the message globally
 * rather than to drop it because it did not match a known input.
 */
export type Rejection = {
  message: string;
  field?: string;
};

/**
 * Which input a server field name belongs to.
 *
 * The mapping exists because the two vocabularies are not the same and should
 * not be forced to be: the API names wire fields, the form names controls.
 * `keyword_mode` has no control of its own — it is set implicitly by typing a
 * keyword — so a complaint about it points at the keyword box, which is the
 * only thing the user can act on.
 */
const FIELD_CONTROL: Record<string, string> = {
  target: "target",
  name: "name",
  type: "type",
  interval_s: "interval",
  timeout_s: "timeout",
  keyword: "keyword",
  keyword_mode: "keyword",
  push_interval_s: "push-interval",
  push_grace_s: "push-grace",
  repeat_after_s: "repeat",
  recovery_threshold: "recovery",
  min_tls_version: "min-tls",
  json_assertion: "json-path",
  "json_assertion.path": "json-path",
  "json_assertion.operator": "json-operator",
  "json_assertion.expected": "json-expected",
  dns: "dns-type",
  "dns.record_type": "dns-type",
  "dns.expected": "dns-expected",
  "dns.resolver": "dns-resolver",
  domain_warn_days: "domain-warn",
  channel_ids: "channels",
};

/**
 * The controls that live inside the collapsed advanced panel.
 *
 * A message placed under one of these is invisible until the panel is opened,
 * and the form deliberately suppresses both of its global notices once a
 * message has been placed — so a rejection naming one of these fields used to
 * disappear entirely: the panel hid it and nothing else said anything. The set
 * is written down once rather than derived from the JSX, because the thing that
 * matters is *which controls are hidden*, and the markup cannot be asked that.
 */
const ADVANCED_CONTROLS = new Set([
  "type",
  "interval",
  "timeout",
  "recovery",
  "keyword",
  "min-tls",
  "json-path",
  "json-operator",
  "json-expected",
]);

/** The numeric values that are lengths of time, by the API field each becomes. */
type DurationKey = "intervalS" | "timeoutS" | "pushIntervalS" | "pushGraceS";
const PROBE_DURATIONS: readonly (readonly [string, DurationKey])[] = [
  ["interval_s", "intervalS"],
  ["timeout_s", "timeoutS"],
];
const PUSH_DURATIONS: readonly (readonly [string, DurationKey])[] = [
  ["push_interval_s", "pushIntervalS"],
  ["push_grace_s", "pushGraceS"],
];

export type AddMonitorFormProps = {
  /** Runs a preview. The caller reports the outcome back through `preview`. */
  onPreview: (values: AddMonitorValues) => void;
  /** Saves. Called only from the submit button. */
  onSubmit: (values: AddMonitorValues) => void;
  /** The current preview phase, owned by the caller. */
  preview: PreviewState;
  /** True while a save is in flight. */
  saving?: boolean;
  /** A save that failed, as the server explained it. */
  saveError?: Rejection | null;
  onCancel?: () => void;
  /** Reports dirty state only; field values never leave for persistence. */
  onDirtyChange?: (dirty: boolean) => void;
  /** Injected in tests. Defaults to the real channel list. */
  loadChannels?: (signal: AbortSignal) => Promise<Channel[]>;
};

const DEFAULTS: AddMonitorValues = {
  name: "",
  target: "",
  type: "",
  intervalS: 60,
  timeoutS: 10,
  keyword: "",
  keywordMode: "absent_ok",
  // An hour, matching the most common thing a push monitor watches: a nightly
  // or hourly cron line. The API has no default of its own — it requires the
  // field — so something has to be offered, and an empty number box that
  // rejects on save is the worst of both.
  pushIntervalS: 3600,
  // The server's own default. Repeated rather than left blank so the value is
  // visible before it is committed: silent grace is how a monitor ends up
  // alerting a minute later than its owner expects.
  pushGraceS: 60,
  repeatAfterS: 900,
  // The server default, shown so the delay before "resolved" is visible.
  recoveryThreshold: 2,
  // No opinion, and never the current default spelled out: a form that
  // pre-selected 1.2 would pin every new monitor to today's floor and quietly
  // make the nullable column unreachable from the UI.
  minTlsVersion: TLS_FLOOR_UNSET,
  jsonPath: "",
  jsonOperator: "equals",
  jsonExpected: "",
  dnsRecordType: "A",
  dnsExpected: "",
  dnsResolver: "",
  // The server's default: a renewal can need a person with the registrar
  // login and a card, which is more lead time than a certificate needs.
  domainWarnDays: 30,
  channelIds: "",
};

export function AddMonitorForm({
  onPreview,
  onSubmit,
  preview,
  saving = false,
  saveError = null,
  onCancel,
  onDirtyChange,
  loadChannels,
}: AddMonitorFormProps) {
  const ids = useId();
  const [repeatText, setRepeatText] = useState("900");
  const [repeatError, setRepeatError] = useState<string>();
  const formRef = useRef<HTMLFormElement>(null);
  const [values, setValues] = useState<AddMonitorValues>(DEFAULTS);
  /*
   * The name, once someone has typed one.
   *
   * Until then it is null and the field shows a suggestion derived from the
   * target — that is what makes the name free rather than a second thing to
   * fill in. The moment it is edited it becomes theirs and the target stops
   * overwriting it.
   *
   * State rather than a ref holding a "touched" flag, even though the flag
   * would never need to re-render on its own: the field's displayed value is
   * derived from it, so it is render input by definition, and a ref read
   * during render is exactly the pattern that leaves the input showing a
   * stale value after a concurrent re-render.
   */
  const [typedName, setTypedName] = useState<string | null>(null);
  /*
   * A duration the API would refuse, caught before anything is sent.
   *
   * The number boxes used to be `type="number"` with a min and a max, and the
   * browser refused an out-of-range value with a bubble of its own. A
   * duration box takes "1.5" with "hours" beside it, which only this form can
   * multiply out, so this form checks the result and places the message under
   * the field the way a server rejection is placed.
   */
  const [localError, setLocalError] = useState<Rejection | null>(null);
  const dirty = repeatText !== "900" || (typedName ?? "") !== "" ||
    Object.keys(DEFAULTS).some((key) => values[key as keyof AddMonitorValues] !== DEFAULTS[key as keyof AddMonitorValues]);
  useEffect(() => { onDirtyChange?.(dirty); }, [dirty, onDirtyChange]);

  const push = isPush(values);
  const dns = values.type === "dns";
  const domain = values.type === "domain";
  const targetEmpty = values.target.trim() === "";
  /*
   * What blocks the save.
   *
   * A push monitor has no target and must never be asked for one, so the
   * usual "type something in the one required box" guard would lock its save
   * button forever. It needs a name instead, which is the only thing it can
   * be recognised by once it is in the list.
   */
  const incomplete = push ? (typedName ?? "").trim() === "" : targetEmpty;
  const name = push
    ? (typedName ?? "")
    : (typedName ?? suggestName(values.target));
  const effective = { ...values, name };

  /*
   * The one rejection currently on screen, whichever half produced it.
   *
   * A save error wins over a preview one because it is the more recent answer
   * to the more committing question; only one can be true of the form as it
   * stands, and showing both would leave two red boxes contradicting each
   * other about the same input.
   */
  const rejection: Rejection | null =
    localError ?? saveError ?? (preview.phase === "rejected" ? preview : null);
  const badControl =
    rejection?.field !== undefined
      ? (FIELD_CONTROL[rejection.field] ?? null)
      : null;

  /*
   * Whether the advanced panel is open.
   *
   * Uncontrolled in the ordinary case — `<details>` already toggles itself, and
   * forcing `open` from state would fight the user's click. State exists only
   * so a *new* rejection about a hidden control can push it open once; after
   * that the panel is theirs again and closing it sticks.
   */
  const [advancedOpen, setAdvancedOpen] = useState(false);
  /*
   * The rejection the panel was last opened for.
   *
   * Keyed on the message as well as the control, because two different
   * complaints about the same box are two different answers and the second one
   * deserves to be seen as much as the first.
   */
  const openedFor = useRef<string | null>(null);
  const hiddenRejection =
    badControl !== null && ADVANCED_CONTROLS.has(badControl)
      ? `${badControl}:${rejection?.message ?? ""}`
      : null;

  useEffect(() => {
    if (hiddenRejection === null) {
      openedFor.current = null;
      return;
    }
    if (openedFor.current === hiddenRejection) return;
    openedFor.current = hiddenRejection;
    setAdvancedOpen(true);
  }, [hiddenRejection]);

  /*
   * Props that mark an input as the one at fault.
   *
   * `aria-invalid` alone says "something here is wrong" without saying what,
   * so the message is tied on with `aria-describedby` and the field's own
   * help text is kept in the list — the explanation of the format is at least
   * as useful when you have just got it wrong.
   */
  const invalidProps = (control: string, describedBy: string) =>
    badControl === control
      ? {
          "aria-invalid": true,
          "aria-describedby": `${describedBy} ${ids}-field-error`,
        }
      : { "aria-describedby": describedBy };

  /** `aria-*` for a control whose only description is a rejection placed under it. */
  const errorProps = (control: string) =>
    badControl === control
      ? { "aria-invalid": true as const, "aria-describedby": `${ids}-field-error` }
      : {};

  /** A length of time over one of the numeric values (DurationField.tsx). */
  const duration = (
    control: string,
    key: DurationKey,
    units: readonly DurationUnit[],
    label: string,
    aria: { "aria-invalid"?: boolean; "aria-describedby"?: string },
  ) => (
    <DurationField
      id={`${ids}-${control}`}
      value={String(values[key])}
      units={units}
      label={label}
      inputProps={aria}
      onChange={(value) => {
        setLocalError(null);
        setValues((v) => ({ ...v, [key]: Number(value) }));
      }}
    />
  );

  /** One of the three assertion controls; without a placeholder it is the operator select. */
  const jsonControl = (
    control: string,
    label: string,
    key: "jsonPath" | "jsonOperator" | "jsonExpected",
    placeholder?: string,
  ) => {
    const props = {
      id: `${ids}-${control}`,
      className: "input",
      value: values[key],
      onChange: (event: { target: { value: string } }) =>
        setValues((v) => ({ ...v, [key]: event.target.value })),
      ...invalidProps(control, `${ids}-json-help`),
    };
    return (
      <div className="field">
        <label className="field-label" htmlFor={props.id}>{label}</label>
        {placeholder === undefined ? (
          <Select {...props}>
            {JSON_OPERATORS.map((op) => <option key={op} value={op}>{op.replace("_", " ")}</option>)}
          </Select>
        ) : (
          <input {...props} placeholder={placeholder} autoComplete="off" spellCheck={false} />
        )}
        <ControlRefusal control={control} badControl={badControl} rejection={rejection} ids={ids} />
      </div>
    );
  };

  const setTarget = (target: string) => setValues((v) => ({ ...v, target }));

  /**
   * Whether every duration on screen is one the API accepts; when one is not,
   * its message is placed under it and it takes focus. Run before a probe as
   * well as before a save: a timeout typed as a word would otherwise reach
   * the probe as nothing at all.
   */
  const durationsAllowed = (): boolean => {
    setLocalError(null);
    const outOfRange = (push ? PUSH_DURATIONS : PROBE_DURATIONS)
      .find(([field, key]) => !durationAllowed(field, values[key]));
    if (outOfRange === undefined) return true;
    const [field] = outOfRange;
    setLocalError({ field, message: DURATION_LIMITS[field].message });
    const control = document.getElementById(`${ids}-${FIELD_CONTROL[field]}`);
    const panel = control?.closest("details");
    if (panel) panel.open = true;
    control?.focus();
    return false;
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (incomplete || saving) return;
    if (!durationsAllowed()) return;
    if (!validRepeat(repeatText)) {
      setRepeatError(REPEAT_ERROR);
      formRef.current?.querySelector<HTMLInputElement>("[data-repeat-input]")?.focus();
      return;
    }
    setRepeatError(undefined);
    onSubmit({ ...effective, repeatAfterS: Number(repeatText) });
  };

  return (
    <form
      className="form-column"
      ref={formRef}
      onSubmit={submit}
      aria-labelledby={`${ids}-heading`}
    >
      <fieldset disabled={saving} className="contents">
      {/*
       * The heading is visually hidden, not removed (SUB-138).
       *
       * The drawer that contains this form already says "Add monitor" in its
       * own header, so the form printed the same instruction twice, one line
       * apart, in two different sizes. What the heading is *for* is naming
       * the form for assistive technology — `aria-labelledby` points at it —
       * and that job does not need pixels.
       */}
      <h2 id={`${ids}-heading`} className="sr-only">
        Add a monitor
      </h2>
      <p className="add-lede">
        {push
          ? "Nothing is dialled for a push monitor. Name it, say how often it should report in, and paste the URL it gets into the job."
          : "Paste an address. SubGlance works out what kind of check it is and fills in the rest — you can change any of it below."}
      </p>

      {/*
       * The target box is removed, not disabled, for a push monitor.
       *
       * The API rejects a push monitor that carries a target at all, so a
       * greyed-out box would be a control that can never be used sitting above
       * an error explaining it must stay empty. Nothing to dial means nothing
       * to ask for.
       */}
      {!push && (
        <div className="field">
          <label className="field-label" htmlFor={`${ids}-target`}>
            What should be watched
          </label>
          <input
            id={`${ids}-target`}
            className="input"
            value={values.target}
            onChange={(event) => setTarget(event.target.value)}
            placeholder={dns || domain ? "example.com" : "example.com, https://example.com/health, or db.example.com:5432"}
            autoComplete="off"
            spellCheck={false}
            required
            {...invalidProps("target", `${ids}-target-help`)}
          />
          <p id={`${ids}-target-help`} className="field-help">
            {dns
              ? "The domain name whose record is checked, without https:// or a port."
              : domain
                ? "The domain whose registration is checked. A name under it, such as www.example.com, checks example.com."
                : "A URL, a hostname, or a host and port. A bare hostname is checked over HTTPS."}
          </p>
          <ControlRefusal
            control="target"
            badControl={badControl}
            rejection={rejection}
            ids={ids}
          />
        </div>
      )}

      <div className="field">
        <label className="field-label" htmlFor={`${ids}-name`}>
          Name
        </label>
        <input
          id={`${ids}-name`}
          className="input"
          value={name}
          onChange={(event) => setTypedName(event.target.value)}
          placeholder={push ? "Nightly backup" : "Taken from the address"}
          autoComplete="off"
          required={push}
          {...invalidProps("name", `${ids}-name-help`)}
        />
        <ControlRefusal
          control="name"
          badControl={badControl}
          rejection={rejection}
          ids={ids}
        />
        <p id={`${ids}-name-help`} className="field-help">
          {push
            ? "Required: there is no address to fall back on. Name it after the job."
            : "Optional. Left alone, it follows the address."}
        </p>
      </div>

      {/*
       * The push window sits outside the advanced panel, unlike every other
       * setting, because for a push monitor it is not advanced: it is the
       * whole definition. A required field hidden behind a disclosure is a
       * form that rejects on save for a reason the user cannot see.
       */}
      {push && (
        <div className="field-grid">
          <div className="field">
            <label className="field-label" htmlFor={`${ids}-push-interval`}>
              Should report every
            </label>
            {duration("push-interval", "pushIntervalS", SECONDS_TO_DAYS, "Should report every",
              invalidProps("push-interval", `${ids}-push-interval-help`))}
            <p id={`${ids}-push-interval-help`} className="field-help">
              How often the job runs: 1 hour for an hourly job, 1 day for a nightly one.
            </p>
            <ControlRefusal
              control="push-interval"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
            />
          </div>

          <div className="field">
            <label className="field-label" htmlFor={`${ids}-push-grace`}>
              Allow it to be late by
            </label>
            {duration("push-grace", "pushGraceS", SECONDS_TO_DAYS, "Allow it to be late by",
              invalidProps("push-grace", `${ids}-push-grace-help`))}
            <p id={`${ids}-push-grace-help`} className="field-help">
              Silence past the interval plus this is a failure. A backup that
              usually takes a few minutes longer needs room here.
            </p>
            <ControlRefusal
              control="push-grace"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
            />
          </div>
        </div>
      )}

      {/*
       * The warning sits outside the advanced panel for the reason the dns
       * record does below: for a domain monitor it is the one setting that
       * decides when anyone hears about it.
       */}
      {domain && (
        <div className="field">
          <label className="field-label" htmlFor={`${ids}-domain-warn`}>
            Warn before it expires
          </label>
          <div className="add-addon">
            <input
              id={`${ids}-domain-warn`}
              className="input"
              type="number"
              min={0}
              max={365}
              value={values.domainWarnDays}
              onChange={(event) =>
                setValues((v) => ({ ...v, domainWarnDays: Number(event.target.value) }))
              }
              {...invalidProps("domain-warn", `${ids}-domain-warn-help`)}
            />
            <span className="add-unit" aria-hidden="true">
              days
            </span>
          </div>
          <p id={`${ids}-domain-warn-help`} className="field-help">
            The expiry date is read from the registry, once a day by default.
            Within this many days of it you are alerted, without it counting
            as downtime; 0 alerts only once it has expired.
          </p>
          <ControlRefusal control="domain-warn" badControl={badControl} rejection={rejection} ids={ids} />
        </div>
      )}

      {/*
       * The record sits outside the advanced panel for the same reason the
       * push window does: for a dns monitor it is the definition, and the
       * API refuses one without a record type.
       */}
      {dns && (
        <div className="field-grid">
          <div className="field">
            <label className="field-label" htmlFor={`${ids}-dns-type`}>
              Record type
            </label>
            <Select
              id={`${ids}-dns-type`}
              className="input"
              value={values.dnsRecordType}
              onChange={(event) =>
                setValues((v) => ({ ...v, dnsRecordType: event.target.value }))
              }
              {...errorProps("dns-type")}
            >
              {DNS_RECORD_TYPES.map((t) => <option key={t} value={t}>{t}</option>)}
            </Select>
            <ControlRefusal control="dns-type" badControl={badControl} rejection={rejection} ids={ids} />
          </div>

          <div className="field">
            <label className="field-label" htmlFor={`${ids}-dns-resolver`}>
              Resolver
            </label>
            <input
              id={`${ids}-dns-resolver`}
              className="input"
              value={values.dnsResolver}
              onChange={(event) =>
                setValues((v) => ({ ...v, dnsResolver: event.target.value }))
              }
              placeholder="The server’s own"
              autoComplete="off"
              spellCheck={false}
              {...invalidProps("dns-resolver", `${ids}-dns-resolver-help`)}
            />
            <p id={`${ids}-dns-resolver-help`} className="field-help">
              {DNS_RESOLVER_HELP}
            </p>
            <ControlRefusal control="dns-resolver" badControl={badControl} rejection={rejection} ids={ids} />
          </div>

          <div className="field field-wide">
            <label className="field-label" htmlFor={`${ids}-dns-expected`}>
              Expected values
            </label>
            <textarea
              id={`${ids}-dns-expected`}
              className="input"
              rows={3}
              value={values.dnsExpected}
              onChange={(event) =>
                setValues((v) => ({ ...v, dnsExpected: event.target.value }))
              }
              placeholder={DNS_EXPECTED_PLACEHOLDER[values.dnsRecordType]}
              spellCheck={false}
              {...invalidProps("dns-expected", `${ids}-dns-expected-help`)}
            />
            <p id={`${ids}-dns-expected-help`} className="field-help">
              {DNS_EXPECTED_HELP[values.dnsRecordType]} {DNS_EMPTY_HELP}
            </p>
            <ControlRefusal control="dns-expected" badControl={badControl} rejection={rejection} ids={ids} />
          </div>
        </div>
      )}

      {/*
       * Collapsed, and collapsed by default. The fields below are real — a
       * keyword check is the difference between "the server is up" and "the
       * site works" — but every one of them shown up front is a question asked
       * of someone who has not yet seen the product do anything.
       */}
      {/*
        `open` is driven from state only so a rejection about a control in here
        can reveal it; `onToggle` writes the user's own clicks straight back, so
        the two never disagree about what is on screen.
      */}
      <details
        className="add-advanced"
        open={advancedOpen}
        onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
      >
        <summary className="add-summary">Advanced options</summary>

        <div className="field-grid">
          <div className="field">
            <label className="field-label" htmlFor={`${ids}-type`}>
              Check type
            </label>
            <Select
              id={`${ids}-type`}
              className="input"
              value={values.type}
              onChange={(event) =>
                setValues((v) => withType(v, event.target.value))
              }
              aria-invalid={badControl === "type" ? true : undefined}
              aria-describedby={
                badControl === "type" ? `${ids}-field-error` : undefined
              }
            >
              <option value="">Work it out from the address</option>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
              <option value="ping">Ping</option>
              <option value="ssl">TLS certificate</option>
              <option value="dns">DNS record</option>
              <option value="domain">Domain registration</option>
              <option value="push">Push — the job reports in</option>
            </Select>
            <ControlRefusal
              control="type"
              badControl={badControl}
              rejection={rejection}
              ids={ids}
            />
          </div>

          {!push && (
            <>
              <div className="field">
                <label className="field-label" htmlFor={`${ids}-interval`}>
                  Check every
                </label>
                {duration("interval", "intervalS", SECONDS_TO_HOURS, "Check every", errorProps("interval"))}
                <ControlRefusal
                  control="interval"
                  badControl={badControl}
                  rejection={rejection}
                  ids={ids}
                />
              </div>

              <div className="field">
                <label className="field-label" htmlFor={`${ids}-timeout`}>
                  Give up after
                </label>
                {duration("timeout", "timeoutS", SECONDS_ONLY, "Give up after", errorProps("timeout"))}
                <ControlRefusal
                  control="timeout"
                  badControl={badControl}
                  rejection={rejection}
                  ids={ids}
                />
              </div>

              <div className="field">
                <label className="field-label" htmlFor={`${ids}-recovery`}>
                  Recover after
                </label>
                <div className="add-addon">
                  <input
                    id={`${ids}-recovery`}
                    className="input"
                    type="number"
                    min={1}
                    max={10}
                    value={values.recoveryThreshold}
                    onChange={(event) =>
                      setValues((v) => ({
                        ...v,
                        recoveryThreshold: Number(event.target.value),
                      }))
                    }
                    {...invalidProps("recovery", `${ids}-recovery-help`)}
                  />
                  <span className="add-unit" aria-hidden="true">
                    passes
                  </span>
                </div>
                <p id={`${ids}-recovery-help`} className="field-help">
                  Passing checks in a row before an incident closes and
                  “resolved” is sent.
                </p>
                <ControlRefusal
                  control="recovery"
                  badControl={badControl}
                  rejection={rejection}
                  ids={ids}
                />
              </div>

              {/* A DNS query and a registry lookup have no TLS and no body to read. */}
              {!dns && !domain && <>
              <TlsFloorField
                id={`${ids}-min-tls`}
                value={values.minTlsVersion}
                onChange={(minTlsVersion) =>
                  setValues((v) => ({ ...v, minTlsVersion }))
                }
                invalid={badControl === "min-tls"}
                {...(badControl === "min-tls"
                  ? { errorId: `${ids}-field-error` }
                  : {})}
              >
                <ControlRefusal
                  control="min-tls"
                  badControl={badControl}
                  rejection={rejection}
                  ids={ids}
                />
              </TlsFloorField>

              <div className="field field-wide">
                <label className="field-label" htmlFor={`${ids}-keyword`}>
                  Body must contain
                </label>
                <input
                  id={`${ids}-keyword`}
                  className="input"
                  value={values.keyword}
                  onChange={(event) =>
                    setValues((v) => ({
                      ...v,
                      keyword: event.target.value,
                      // A keyword with the mode left at "ignore" is a field that
                      // silently does nothing — the exact trap DESIGN.md §7.2 is
                      // about. Typing one means you want it checked.
                      keywordMode:
                        v.keywordMode === "absent_ok" &&
                        event.target.value !== ""
                          ? "must_contain"
                          : v.keywordMode,
                    }))
                  }
                  placeholder="Leave empty to check only that it answers"
                  autoComplete="off"
                  aria-invalid={badControl === "keyword" ? true : undefined}
                  aria-describedby={
                    badControl === "keyword" ? `${ids}-field-error` : undefined
                  }
                />
                <ControlRefusal
                  control="keyword"
                  badControl={badControl}
                  rejection={rejection}
                  ids={ids}
                />
              </div>
              </>}

              {/* Only an HTTP check reads a body; an unset type is inferred and may be one. */}
              {(values.type === "" || values.type === "http") && <>
              {/*
               * One field of a JSON body, for the health endpoint that answers
               * 200 and says in a field that it is not healthy. Three plain
               * controls rather than one expression box: the API takes exactly
               * one path, one operator and one value, and the form should not
               * suggest it can take more.
               */}
              {jsonControl("json-path", "JSON field", "jsonPath", "checks.db.status")}
              {jsonControl("json-operator", "Must", "jsonOperator")}
              {values.jsonOperator !== "exists" && jsonControl("json-expected", "Value", "jsonExpected", "up")}
              <p id={`${ids}-json-help`} className="field-help field-wide">
                {JSON_HELP}
              </p>
              </>}
            </>
          )}
        </div>
      </details>
      <AlertingSection>
        <ChannelPicker value={channelIdsFromText(values.channelIds)}
          onChange={(chosen) => setValues((v) => ({ ...v, channelIds: channelIdsText(chosen) }))}
          error={badControl === "channels" ? rejection?.message : undefined}
          failedNote="The monitor can still be saved; it then alerts through the default channel, and its channels can be chosen later in its edit form."
          load={loadChannels} />
        <RepeatAlertField value={repeatText} onChange={setRepeatText} error={repeatError ?? (saveError?.field === "repeat_after_s" ? saveError.message : undefined)} />
      </AlertingSection>
      </fieldset>

      {saving && <p className="field-help">A save in progress may still complete if you close this form.</p>}
      <div className="button-row">
        {/*
         * No "Test it" for a push monitor. The button probes a target, and a
         * push monitor has none: the only way to test one is to run the job,
         * which is what the curl line handed over after saving is for.
         */}
        {!push && (
          <button
            type="button"
            className="button"
            onClick={() => { if (durationsAllowed()) onPreview(effective); }}
            // Deliberately NOT disabled while a probe is in flight. A check can
            // take the full timeout, and the most common reason to press this
            // twice is that the typo became obvious the moment the first one
            // started — locking the button makes the user wait out a request
            // whose answer they already know is useless. The caller aborts the
            // previous probe, so a late answer cannot overwrite a newer one.
            disabled={targetEmpty || saving}
          >
            {/* The label keeps the button's width rather than swapping in a
              spinner that resizes it (§7.1). */}
            {preview.phase === "checking" ? "Testing…" : "Test it"}
          </button>
        )}
        <button
          type="submit"
          className="button button--primary"
          disabled={incomplete || saving}
        >
          {saving ? "Saving…" : "Save monitor"}
        </button>
        {onCancel !== undefined && (
          <button
            type="button"
            className="button button--quiet"
            onClick={onCancel}
          >
            Cancel
          </button>
        )}
      </div>

      {/*
       * One live region for the preview, and it is polite.
       *
       * The result appears because the user pressed a button and is waiting
       * for it, so `assertive` would interrupt them to say the thing they
       * asked for. A save error is a different matter: it happened to them
       * rather than for them, and it gets `role="alert"` below.
       */}
      <div role="status" aria-live="polite" className="result-region">
        <PreviewNotice preview={preview} placed={badControl !== null} />
      </div>

      {/*
       * Whatever could not be placed. A rate limit, a network failure and a
       * malformed response are not about any input, and neither is a
       * complaint about a field this form does not render — putting those
       * under an arbitrary box would be worse than leaving them here.
       */}
      {saveError !== null && badControl === null && (
        <p role="alert" className="result result--bad">
          Could not save: {saveError.message}
        </p>
      )}
    </form>
  );
}

/**
 * The message for one input, rendered only beneath the input at fault.
 *
 * `role="alert"` rather than a live region on a wrapper: the element appears
 * when the error does, so there is nothing to announce until it exists, and an
 * empty live region that later fills is the pattern screen readers most often
 * miss. It carries a stable id because the input points at it with
 * `aria-describedby`, and only one can be on screen at a time — the server
 * rejects on the first problem it finds, so a second id would never resolve.
 */
function ControlRefusal({
  control,
  badControl,
  rejection,
  ids,
}: {
  control: string;
  badControl: string | null;
  rejection: Rejection | null;
  ids: string;
}) {
  if (badControl !== control || rejection === null) return null;
  return (
    <FieldError id={`${ids}-field-error`}>
      {rejection.message}
    </FieldError>
  );
}

function PreviewNotice({
  preview,
  placed,
}: {
  preview: PreviewState;
  placed: boolean;
}) {
  switch (preview.phase) {
    case "idle":
      return null;
    case "checking":
      return <p className="result">Checking…</p>;
    case "rejected":
      // Not a failed check: nothing was contacted. Saying so keeps someone
      // from going to look at a server that was never asked anything.
      //
      // Suppressed once the message has been placed under an input: the same
      // sentence in two places reads as two problems, and the copy down here
      // is the one that is further from the box you have to fix.
      if (placed) return null;
      return <p className="result result--bad">{preview.message}</p>;
    case "done":
      return <PreviewSummary result={preview.result} />;
  }
}

function PreviewSummary({ result }: { result: PreviewResult }) {
  return (
    <p
      className={
        result.ok ? "result result--good" : "result result--bad"
      }
    >
      {describePreview(result)}{" "}
      <span className="add-result-kind">
        {/* What it decided to check, spelled out. This is the field the form
            stopped asking for, so it has to be visible somewhere before the
            user commits. */}
        Checked as {labelForType(result.type)}.
      </span>
    </p>
  );
}

/**
 * The values after choosing a check type.
 *
 * A domain monitor refuses an interval under six hours, so choosing it moves
 * a shorter one to a day, the server's own default, rather than letting the
 * save fail on a field the user never touched. Leaving it puts the day back
 * to the form's default, because a day is not what anyone expects of an
 * HTTP check they did not configure.
 */
export function withType(values: AddMonitorValues, type: string): AddMonitorValues {
  if (type === "domain" && values.intervalS < DOMAIN_MIN_INTERVAL_S) {
    return { ...values, type, intervalS: DOMAIN_INTERVAL_S };
  }
  if (values.type === "domain" && type !== "domain" && values.intervalS === DOMAIN_INTERVAL_S) {
    return { ...values, type, intervalS: DEFAULTS.intervalS };
  }
  return { ...values, type };
}

/** The shortest interval the API takes for a domain monitor, and its default. */
const DOMAIN_MIN_INTERVAL_S = 6 * 3600;
const DOMAIN_INTERVAL_S = 86400;

function labelForType(type: string): string {
  switch (type) {
    case "http":
      return "an HTTP request";
    case "tcp":
      return "a TCP connection";
    case "ping":
      return "a ping";
    case "ssl":
      return "a TLS certificate check";
    case "dns":
      return "a DNS query";
    case "domain":
      return "a domain registration lookup";
    default:
      return type;
  }
}
