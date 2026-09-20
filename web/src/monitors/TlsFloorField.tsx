import type { ReactNode } from "react";
import { TLS_FLOOR_OPTIONS } from "./tlsFloor";

/**
 * The TLS floor control, and the sentences that make it mean something.
 *
 * **The explanation is the control.** "Minimum TLS version" reads like a
 * client setting — the way the same words read in a browser — right up until
 * you notice that making the check fail is the entire point of raising it. A
 * dropdown with four numbers in it communicates neither of the two opposite
 * reasons to touch this, so the help text below states both, in the register
 * of docs/using-subglance.md § Choosing a TLS floor and much shorter.
 *
 * One component rather than the same markup pasted into the add form and the
 * edit form, because the wording is the part that has to stay identical: two
 * copies of an explanation are two explanations, and the second one to be
 * edited is the one somebody reads.
 *
 * `""` is the fifth option and it is not a version. It means the monitor has
 * no opinion, which is why the column is nullable: a monitor that says nothing
 * follows SubGlance if the default floor moves, and one that says `1.2` does
 * not. Neither form may turn it into `1.2` on submit or on load.
 */

export type TlsFloorFieldProps = {
  /** The select's id. The help paragraph derives its own id from it. */
  id: string;
  /** `""` for no opinion; otherwise "1.0" to "1.3". */
  value: string;
  onChange: (value: string) => void;
  /** Set when a rejection names this control. */
  invalid?: boolean;
  /** The id of the message element, when there is one. */
  errorId?: string;
  /** The message itself, rendered under the select by the caller. */
  children?: ReactNode;
};

export function TlsFloorField({
  id,
  value,
  onChange,
  invalid = false,
  errorId,
  children,
}: TlsFloorFieldProps) {
  const helpId = `${id}-help`;
  return (
    <div className="add-field add-field-wide">
      <label className="add-label" htmlFor={id}>
        Minimum TLS version
      </label>
      <select
        id={id}
        className="add-input"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={invalid ? true : undefined}
        // The help text stays in the list when a message is added: the
        // explanation of what this setting does is at least as useful once you
        // have just been told the value was refused.
        aria-describedby={
          errorId !== undefined ? `${helpId} ${errorId}` : helpId
        }
      >
        {TLS_FLOOR_OPTIONS.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
      {children}
      <p id={helpId} className="add-help">
        This is the floor SubGlance dials with, not a description of what the
        server offers. Raising it is an assertion: set 1.3 and the check goes
        red the day the server starts offering 1.2 again, which is the monitor
        doing its job rather than breaking. Lowering it is the opposite case —
        an appliance that only speaks TLS 1.0 cannot be checked at the default
        floor at all, and the permanent outage it reports is really a refused
        handshake. A failure names this floor rather than the server&rsquo;s:
        the peer answers a too-low ClientHello with an alert instead of saying
        which versions it would have accepted. Left at no opinion, the monitor
        follows the SubGlance default instead of pinning today&rsquo;s.
      </p>
    </div>
  );
}
