import { useEffect } from "react";

/**
 * The tab label, and the name of every entry in the back history.
 *
 * `index.html` hard-codes `<title>SubGlance</title>` and nothing ever changed
 * it, so a client-side navigation produced a history entry indistinguishable
 * from the one before it. Pressing and holding Back offered a column of
 * identical rows, and a user with a dozen tabs open could not tell which one
 * held the monitor that was down.
 *
 * The product name goes last. A tab is truncated from the right, so leading
 * with it would spend the only visible characters on the word that is the same
 * in every tab.
 */
export const APP_NAME = "SubGlance";

/** `"api.example.com — SubGlance"`, or just the product name for no page. */
export function documentTitle(page: string | null): string {
  const trimmed = page?.trim();
  return trimmed ? `${trimmed} — ${APP_NAME}` : APP_NAME;
}

/**
 * Names the current screen for as long as it is mounted.
 *
 * Restores the bare product name on unmount rather than leaving the last
 * screen's name behind: a monitor that has been navigated away from must not
 * keep labelling the tab, which is exactly what a fire-and-forget assignment
 * in an effect would do.
 */
export function useDocumentTitle(page: string | null): void {
  useEffect(() => {
    document.title = documentTitle(page);
    return () => {
      document.title = documentTitle(null);
    };
  }, [page]);
}
