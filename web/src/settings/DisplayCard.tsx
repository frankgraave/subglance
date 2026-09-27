import { Card, Panel } from "../components/Card";
import { ThemeToggle } from "../components/ThemeToggle";
import { CardColumnsSwitcher } from "../shell/CardColumnsSwitcher";
import { LayoutSwitcher } from "../shell/LayoutSwitcher";
import type { CardColumns, LayoutId } from "../shell/preferences";
import type { ThemePreference } from "../theme/theme";

/**
 * The display preferences, as `App` owns them. Every one of them lives in
 * this browser's `localStorage` and nowhere else (SUB-28, decision 2): no
 * schema, no endpoint, no round-trip before first paint.
 */
export type DisplayPreferences = {
  theme: ThemePreference;
  onThemeChange: (next: ThemePreference) => void;
  layout: LayoutId;
  /** The layout on screen when the viewport overrode the preference. */
  effectiveLayout?: LayoutId;
  onLayoutChange: (next: LayoutId) => void;
  cardColumns: CardColumns;
  onCardColumnsChange: (next: CardColumns) => void;
};

/**
 * The Display section of the settings page.
 *
 * **It reuses the three controls the product already draws** — the theme
 * glyphs from the masthead, the layout switcher from the masthead, and the
 * cards-per-row bars from the dashboard's tools row — rather than a second,
 * word-only set. A preference that looks one way where it is used and
 * another way where it is configured is two things to learn, and the two
 * copies would drift the way the four hand-rolled segmented controls did
 * before `SegmentedControl` existed.
 *
 * **Why it exists when the masthead already has two of these.** Cards per
 * row is otherwise only reachable while the dashboard is in the Cards
 * layout, so this is the one place all three can be seen at once. And the
 * fact that none of them follow the account has to be written down
 * somewhere: someone who signs in from a second machine and finds the
 * default theme would otherwise read it as a lost setting.
 *
 * **The layout shows what is on screen**, like the masthead does, so the
 * two copies on this page can never disagree about which segment is pressed.
 */
export function DisplayCard({ prefs }: { prefs: DisplayPreferences }) {
  return (
    <Card
      title="Display"
      className="display-card"
      note="Saved in this browser, not with your account."
    >
      <Panel label="Theme">
        <ThemeToggle preference={prefs.theme} onChange={prefs.onThemeChange} />
      </Panel>
      <Panel label="Dashboard layout">
        <LayoutSwitcher
          layout={prefs.layout}
          effective={prefs.effectiveLayout}
          onChange={prefs.onLayoutChange}
        />
      </Panel>
      <Panel label="Cards per row">
        <CardColumnsSwitcher
          value={prefs.cardColumns}
          onChange={prefs.onCardColumnsChange}
        />
      </Panel>
    </Card>
  );
}
