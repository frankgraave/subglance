import { useNow } from "../live/useNow";
import { hostOfflineText } from "./offlineState";

/**
 * The dashboard's line for "this server lost its own connection" (SUB-151).
 *
 * Design reasoning:
 *
 * 1. **It explains, it does not replace.** The monitors stay exactly as they
 *    are: amber warnings with their real error text. Their last known state is
 *    still true — the checks did fail — and hiding them would make the screen
 *    lie in the other direction. What was missing was the one sentence that
 *    says why they are all amber at once.
 * 2. **The connection badge's look, deliberately.** Both are statements about
 *    whether the data on screen can be taken at face value, not about any
 *    monitor, and both are `--warn`, never `--down`: a server that cannot see
 *    out is not an outage of the things it watches. Reusing `.conn-badge`
 *    also costs no stylesheet, and the stylesheet budget has no room.
 *    The dot is held still, though (connection.css): the reconnecting badge
 *    is transient, while this line can stay up for hours, and a dot that
 *    breathes for hours is animation at rest (DESIGN.md rule 2).
 * 3. **`role="status"`, polite, and always mounted.** A live region only
 *    announces changes to a region that already existed, so the empty one is
 *    rendered while online, the same as ConnectionBadge.
 * 4. **Only what the endpoint vouches for.** See useHostOfflineSince.
 */
export function HostOfflineNotice({ since }: { since: number | null }) {
  const now = useNow();
  if (since === null) {
    return <div role="status" aria-live="polite" className="sr-only" />;
  }
  return (
    <div role="status" aria-live="polite" className="conn-badge" data-state="host-offline">
      <span className="conn-badge-dot" aria-hidden="true" />
      <span>
        {hostOfflineText(since, now)}
        <span className="conn-badge-age">
          {" "}
          · failed network checks are held as warnings, not outages
        </span>
      </span>
    </div>
  );
}
