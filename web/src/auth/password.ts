/**
 * How strong a password is, in the only terms this product can honestly use.
 *
 * **Length, and nothing else.** NIST SP 800-63B builds its own strength
 * discussion "primarily on password length", and Revision 4 goes further: a
 * verifier *shall not* impose composition rules, because demanding a digit and
 * a symbol produces predictable substitutions (`password` becomes `P@ssw0rd`)
 * without meaningfully adding entropy. A meter that rewards those characters
 * is therefore not measuring strength, it is coaching the user toward the
 * exact patterns a cracking dictionary already expands.
 *
 * So this returns bands of length, and the copy says what the band means in
 * words rather than colouring a bar and leaving the user to guess. The server
 * is still the authority on what is accepted; this only decides what to draw
 * and whether the submit button is worth enabling.
 *
 * The bands are deliberately coarse. A meter with fine gradations invites
 * people to type until it turns green, which makes the threshold the target
 * — and the threshold is a floor, not a goal.
 */

import { MIN_PASSWORD_LENGTH } from "./api";

export type PasswordStrength = "empty" | "short" | "ok" | "strong";

/**
 * The bands, in characters.
 *
 * `strong` starts at 20 rather than at some multiple of the minimum: a
 * passphrase of three or four ordinary words lands there naturally, and that
 * is the behaviour worth praising. Below the server's minimum there is only
 * one band, because "nearly long enough" is not a distinction that helps —
 * it is still rejected.
 */
const STRONG_LENGTH = 20;

export function passwordStrength(password: string): PasswordStrength {
  // Code points, not UTF-16 units, and the server counts runes too: an emoji
  // or an accented character written as a surrogate pair would otherwise
  // score two here and one there, so the meter would say "long enough" about
  // a password the server rejects.
  const length = [...password].length;
  if (length === 0) return "empty";
  if (length < MIN_PASSWORD_LENGTH) return "short";
  if (length < STRONG_LENGTH) return "ok";
  return "strong";
}

/**
 * What to tell the user about that band.
 *
 * Phrased as a fact plus a reason, never as a scolding. The `short` case says
 * how many characters are still missing rather than repeating the minimum,
 * because the user can already see what they typed and what they need is the
 * remainder.
 */
export function describeStrength(password: string): string {
  const length = [...password].length;
  switch (passwordStrength(password)) {
    case "empty":
      return `At least ${MIN_PASSWORD_LENGTH} characters. A few ordinary words beat a short, cryptic one.`;
    case "short": {
      const missing = MIN_PASSWORD_LENGTH - length;
      return `${missing} more character${missing === 1 ? "" : "s"} to go.`;
    }
    case "ok":
      return "Long enough. A couple more words would make it much harder to crack.";
    case "strong":
      return "Good length — this is the part that actually matters.";
  }
}
