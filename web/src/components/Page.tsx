import type { ReactNode } from "react";
import type { PageWidth } from "../shell/pages";

/**
 * The page frame: one visible title and one width rule for every screen
 * (SUB-182).
 *
 * The title is a real, visible `h1` on the page type role. It used to be
 * visually hidden on three screens and a card's title on two more, so the
 * level-one heading was a different size, a different word and sometimes
 * invisible depending on where you were. The cards under it are `h2`.
 *
 * `title={null}` is for a screen that draws its own `h1` — a monitor's page,
 * whose title is the monitor's name and carries its state beside it. That
 * heading wears `page-title` too, so it sits at the same level as every
 * other page's.
 *
 * The width is `PageWidth`, set per route in `shell/pages.ts` rather than per
 * screen in its stylesheet; see there for why there are exactly two.
 */
export function Page({
  title,
  width,
  children,
}: {
  title: string | null;
  width: PageWidth;
  children: ReactNode;
}) {
  return (
    <div className="page" data-width={width}>
      {title !== null && <h1 className="page-title">{title}</h1>}
      {children}
    </div>
  );
}
