import type { ReactNode } from "react";
import type { PageWidth } from "../shell/pages";

/**
 * The page frame: one width rule for every screen (SUB-182).
 *
 * It used to draw the page's `h1` as well, at the top of the content. The
 * title is the masthead's now (SUB-207): a list screen stacked the bar, the
 * page toolbar and the title before its first card, and the bar had the room
 * for the title all along. A screen still does not title itself — the
 * masthead reads the same table this frame reads its width from, so a screen
 * that drew an `h1` of its own would be the second one on the page.
 *
 * The width is `PageWidth`, set per route in `shell/pages.ts` rather than per
 * screen in its stylesheet; see there for why there are exactly three.
 */
export function Page({
  width,
  children,
}: {
  width: PageWidth;
  children: ReactNode;
}) {
  return (
    <div className="page" data-width={width}>
      {children}
    </div>
  );
}
