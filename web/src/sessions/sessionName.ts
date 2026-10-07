import type { BrowserSession } from "./api";

/**
 * What a person calls a session: "Firefox on macOS".
 *
 * The server reads both from the User-Agent header and leaves either empty
 * when it does not recognise it. Then the header itself is the best name
 * there is, so it goes in the facts line rather than being hidden.
 */
export function sessionName(session: BrowserSession): string {
  if (session.browser && session.platform) return `${session.browser} on ${session.platform}`;
  return session.browser || session.platform || "Unrecognised browser";
}
