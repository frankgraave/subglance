/**
 * Tags as a person types them, and as the API carries them.
 *
 * Its own module rather than an export from the form: a component file that
 * also exports functions breaks fast refresh, and these two are the pair worth
 * testing without a renderer — the parsing rule below is the whole reason a
 * tag value may contain a colon.
 */

/**
 * `{env: "prod", team: "payments"}` as one `key:value` per line.
 *
 * One per line rather than comma-separated, and that is the whole design of
 * this pair. A comma separator cannot round-trip a value that contains a
 * comma: `{note: "a,b", env: "prod"}` renders as `note:a,b, env:prod`, which
 * reads back as three entries, one of which is the bare word `b`. The user
 * then cannot edit any other tag on that monitor, because the text they never
 * touched no longer parses.
 *
 * A newline can be ruled out of a value in a way a comma cannot: the store
 * rejects control characters in tag keys and values, so a line break is never
 * part of one and splitting on it is lossless. The colon still splits at the
 * first occurrence only, so `url:https://example.com` survives too.
 */
export function tagsToText(tags: Record<string, string>): string {
  return Object.entries(tags)
    .map(([key, value]) => `${key}:${value}`)
    .join("\n");
}

/**
 * The inverse. Returns null for text that is not a tag list, so the form can
 * refuse rather than sending something the server will reject with a message
 * about a field the user cannot see.
 */
export function textToTags(text: string): Record<string, string> | null {
  const out: Record<string, string> = {};
  /*
   * Newline is the only separator, and a comma is deliberately NOT one.
   *
   * Accepting both looks generous and quietly destroys the guarantee the line
   * format exists for: `note:a,b` would split again, and the value a user can
   * see in the box would stop being the value that gets saved. A format that
   * round-trips only sometimes is worse than one that asks for a line break,
   * because the failure is invisible until a tag is silently cut in half.
   */
  for (const piece of text.split("\n")) {
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
