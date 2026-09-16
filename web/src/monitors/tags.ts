/**
 * Tags as a person types them, and as the API carries them.
 *
 * Its own module rather than an export from the form: a component file that
 * also exports functions breaks fast refresh, and these two are the pair worth
 * testing without a renderer — the parsing rule below is the whole reason a
 * tag value may contain a colon.
 */

/**
 * `{env: "prod", team: "payments"}` as `env:prod, team:payments`.
 *
 * `key:value` is how a person writes a tag; the API carries an object so a
 * value may contain a colon. The split below therefore takes only the first
 * colon, which keeps `url:https://example.com` intact.
 */
export function tagsToText(tags: Record<string, string>): string {
  return Object.entries(tags)
    .map(([key, value]) => `${key}:${value}`)
    .join(", ");
}

/**
 * The inverse. Returns null for text that is not a tag list, so the form can
 * refuse rather than sending something the server will reject with a message
 * about a field the user cannot see.
 */
export function textToTags(text: string): Record<string, string> | null {
  const out: Record<string, string> = {};
  for (const piece of text.split(",")) {
    const entry = piece.trim();
    if (entry === "") continue;
    const at = entry.indexOf(":");
    if (at <= 0) return null;
    const key = entry.slice(0, at).trim();
    const value = entry.slice(at + 1).trim();
    if (key === "" || value === "") return null;
    out[key] = value;
  }
  return out;
}
