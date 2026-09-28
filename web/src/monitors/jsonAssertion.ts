/**
 * The JSON body assertion as a form holds it: three strings.
 *
 * The API takes `expected` as a JSON value, so "1" and 1 stay apart. A form
 * field holds text, and asking people to type `"up"` with the quotes for the
 * common case would make the common case the awkward one. So the text is read
 * as JSON when it is a number, `true`, `false`, `null` or a quoted string, and
 * as a plain string otherwise: `up` is the string "up", `1` the number 1, and
 * `"1"` the string "1".
 */

export type JsonAssertion = {
  path: string;
  operator: string;
  expected?: unknown;
};

/** The API's operators. Each is shown as itself with a space for the underscore. */
export const JSON_OPERATORS = ["equals", "not_equals", "exists", "less_than", "greater_than"] as const;

/** The help under the assertion controls, shared so both forms say the same. */
export const JSON_HELP =
  'Optional. Dots and indexes reach in: items[0].ok. 1 is a number, "1" a string.';

/** Form text to a JSON value, as described above. */
export function readExpected(text: string): unknown {
  const trimmed = text.trim();
  try {
    const value: unknown = JSON.parse(trimmed);
    if (value === null || typeof value !== "object") return value;
  } catch {
    // Not JSON: a bare word, which is meant as a string.
  }
  return text;
}

/**
 * Why the text cannot be sent as it reads, or `null` when it can.
 *
 * `JSON.parse` turns a number into a double, so `0.1234567890123456789` or
 * `9007199254740993` would reach the API as a different, rounded number, and
 * the check would compare against a value nobody typed. Sending the text as a
 * string instead would change the JSON type, which is worse. So such a number
 * is refused before anything is sent.
 */
export function expectedProblem(text: string): string | null {
  const value = readExpected(text);
  if (typeof value !== "number" || decimal(text.trim()) === decimal(String(value))) return null;
  return `${text.trim()} has more digits than a browser can hold exactly; it would be sent as ${String(value)}.`;
}

/** A number literal as sign, significant digits and power of ten, so 1.50 and 15e-1 compare equal. */
function decimal(literal: string): string {
  const [mantissa, exp = "0"] = literal.toLowerCase().split("e");
  const [whole, fraction = ""] = mantissa.replace("-", "").split(".");
  const digits = (whole + fraction).replace(/^0+/, "");
  const trimmed = digits.replace(/0+$/, "");
  if (trimmed === "") return "0";
  const sign = mantissa.startsWith("-") ? "-" : "";
  return `${sign}${trimmed}e${Number(exp) - fraction.length + digits.length - trimmed.length}`;
}

/** A JSON value back to the text that reads as it. */
export function expectedText(value: unknown): string {
  if (value === undefined) return "";
  if (typeof value === "string" && readExpected(value) === value) return value;
  return JSON.stringify(value);
}

/**
 * The assertion the three fields describe, or `null` for none.
 *
 * An empty path means no assertion; the operator and value are then ignored
 * rather than reported, because they are only defaults the form shows.
 */
export function assertionFrom(path: string, operator: string, expected: string): JsonAssertion | null {
  if (path.trim() === "") return null;
  return operator === "exists"
    ? { path: path.trim(), operator }
    : { path: path.trim(), operator, expected: readExpected(expected) };
}
