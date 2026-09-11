/**
 * A value we do not have, drawn as an em dash.
 *
 * Never `0`. A monitor that has never reported a latency and one that answered
 * instantly are different facts, and rendering both as `0 ms` makes the
 * dashboard confidently wrong. The dash is decorative; the real reason is in
 * the accessible text beside it.
 */
export function Unknown({ what }: { what: string }) {
  return (
    <>
      <span aria-hidden="true">—</span>
      <span className="sr-only">No {what} data</span>
    </>
  );
}
