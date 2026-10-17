import { useEffect, useRef, type ReactNode } from "react";
import type { LedState } from "./ledState";

/**
 * A list's tabs with their counts, at the head of the card they narrow: the
 * dashboard's statuses (All 14 · Down 3 · Up 9) and the incidents screen's
 * scope (All 5 · Open 1 · Resolved 4), drawn by one component so the two
 * read as one control (SUB-183, SUB-207).
 *
 * Buttons with `aria-pressed` in a labelled group, not `role="tab"`: a tab
 * promises a panel of its own, and these narrow one list in place. It is the
 * same choice `SegmentedControl` makes for the same reason, and arrow-key
 * semantics would fight the controls beside it.
 *
 * On a phone the row scrolls sideways when the tabs do not fit, and a row
 * that scrolls has to say so: a tab cut off at the edge of the row, with
 * nothing beside it, reads as the end of the list. `data-more` names the
 * sides with more tabs past them ("start", "end", or both) and the
 * stylesheet fades the row out on those sides, so the last visible tab
 * dissolves rather than ending on a hard cut, and the fade goes when the
 * row is scrolled to that end.
 *
 * The caller decides what a press means. The dashboard turns a press on the
 * selected status back into All; the incidents screen does the same with its
 * scope. That is why `onChange` reports the key that was pressed rather than
 * the next state.
 */
export type ListTab<K extends string> = {
  key: K;
  label: string;
  /**
   * The count beside the word. Left out while it is not known yet — a
   * history still loading — rather than drawn as a zero it has not measured.
   */
  count?: ReactNode;
  /** A colour key beside the word, decorative: the word carries it (DESIGN.md §2.3). */
  state?: LedState;
};

export function ListTabs<K extends string>({
  label,
  tabs,
  value,
  onPress,
}: {
  /** The group's accessible name: what the tabs narrow by. */
  label: string;
  tabs: readonly ListTab<K>[];
  value: K;
  onPress: (key: K) => void;
}) {
  const row = useRef<HTMLDivElement>(null);
  // The tabs arrive as a fresh array on every render (the dashboard renders
  // on every heartbeat); which tabs there are is what the listeners depend on.
  const shape = tabs.map((tab) => tab.key).join(" ");
  useEffect(() => {
    const el = row.current;
    if (el === null) return;
    const mark = () => {
      // A pixel of slack: a fractional scroll width can leave the end a
      // subpixel short of the right edge when the row is scrolled all the way.
      const sides = [
        el.scrollLeft > 1 ? "start" : "",
        el.scrollLeft + el.clientWidth < el.scrollWidth - 1 ? "end" : "",
      ].filter(Boolean).join(" ");
      if (sides === "") el.removeAttribute("data-more");
      else el.setAttribute("data-more", sides);
    };
    mark();
    el.addEventListener("scroll", mark, { passive: true });
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(mark);
    observer?.observe(el);
    // The counts change the tabs' widths without resizing the row itself.
    for (const tab of el.children) observer?.observe(tab);
    return () => {
      el.removeEventListener("scroll", mark);
      observer?.disconnect();
    };
  }, [shape]);

  return (
    <div ref={row} className="mon-tabs" role="group" aria-label={label}>
      {tabs.map((tab) => (
        <button
          key={tab.key}
          type="button"
          className="mon-tab"
          aria-pressed={value === tab.key}
          onClick={() => onPress(tab.key)}
        >
          {tab.state === undefined ? null : (
            <span className="mon-count-dot" data-state={tab.state} aria-hidden="true" />
          )}
          {tab.label}
          {tab.count === undefined ? null : (
            <>
              {" "}
              <span className="mon-tab-count">{tab.count}</span>
            </>
          )}
        </button>
      ))}
    </div>
  );
}
