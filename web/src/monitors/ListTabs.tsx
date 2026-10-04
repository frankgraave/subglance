import type { ReactNode } from "react";
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
  return (
    <div className="mon-tabs" role="group" aria-label={label}>
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
