/**
 * Which check settings a type reads.
 *
 * Only an HTTP check sends a request and reads the response, so only it reads
 * the method, the expected status, the keyword, redirects, the headers and
 * the body. Only an HTTP or TLS certificate check makes a TLS handshake, so
 * only those read the TLS floor and the certificate warning. TCP, ping, DNS
 * and domain checks read the target and the timeout.
 *
 * A setting offered on a type that never reads it is a control that does
 * nothing (DESIGN.md §10): a keyword on a TCP monitor passes whatever the
 * service answers. The API refuses such a value, so both forms offer, send
 * and preview a setting only on a type that reads it, from this one list.
 *
 * An empty type is the add form's "work it out from the address", which may
 * still become an HTTP check, so it is offered everything.
 */

/** Read only by an HTTP check. */
const HTTP_SETTINGS: ReadonlySet<string> = new Set([
  "method", "expected_status", "keyword", "keyword_mode", "follow_redirects", "headers", "body",
]);

/** Read only by a check that makes a TLS handshake. */
const TLS_SETTINGS: ReadonlySet<string> = new Set(["ssl_warn_days", "min_tls_version"]);

/** Whether a check of this type reads the request and response settings. */
export function readsHttpSettings(type: string): boolean {
  return type === "" || type === "http";
}

/** Whether a check of this type makes a TLS handshake. */
export function readsTlsSettings(type: string): boolean {
  return type === "" || type === "http" || type === "ssl";
}

/**
 * Whether a check of this type reads the setting sent as `key`. A key that
 * is not a type-bound check setting (a name, a tag) is read by every type.
 */
export function readsSetting(type: string, key: string): boolean {
  if (HTTP_SETTINGS.has(key)) return readsHttpSettings(type);
  if (TLS_SETTINGS.has(key)) return readsTlsSettings(type);
  return true;
}

/** The entries of `settings` a check of this type reads. */
export function settingsReadBy<T extends object>(type: string, settings: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(settings).filter(([key]) => readsSetting(type, key)),
  ) as Partial<T>;
}
