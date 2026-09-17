/**
 * The component specimens for the living style guide.
 *
 * Rendered to static HTML by `scripts/build-styleguide.mjs` through Vite's
 * SSR loader, so every specimen is the real component with the real class
 * names, against the real built stylesheet. A hand-drawn imitation of a chip
 * would document what someone believed a chip looked like; this documents the
 * chip.
 *
 * Fixtures only. Nothing here reaches a backend or a store, which is also why
 * the data-owning views (MonitorsView, IncidentsView) are not on the page:
 * they need fetch, routing and time, and a static render of them would be a
 * screenshot with the wiring cut.
 */
import type { ReactNode } from "react";
import { Card, Panel } from "./components/Card";
import {
  CountChip,
  EmptyAvatar,
  MetaChip,
  StateChip,
  StatusChip,
} from "./components/Chip";
import { IconPause, IconPencil, IconRefresh, IconTrash } from "./components/icons";
import { PlusIcon, SearchIcon, SidebarIcon } from "./shell/icons";
import { IconTile } from "./components/IconTile";
import { PanelList, PanelRow } from "./components/PanelList";
import { SegmentedControl } from "./components/SegmentedControl";
import { Value } from "./components/Value";
import { Led } from "./monitors/Led";

export type Specimen = {
  id: string;
  title: string;
  /** Where the component lives, so the reader can go from the picture to the code. */
  source: string;
  /** The rule this component carries, in one or two sentences. */
  rule: string;
  node: ReactNode;
};

/*
 * Not a component: a plain function that returns markup. This file exports
 * data, and the fast-refresh lint refuses a component export beside it.
 * Nothing here is ever mounted in the app, so refresh does not apply.
 */
const row = (...children: ReactNode[]): ReactNode => (
  <div className="sg-specimen-row">{children}</div>
);

export const specimens: Specimen[] = [
  {
    id: "led",
    title: "The LED",
    source: "web/src/monitors/Led.tsx",
    rule:
      "The brand mark. 20x7, a horizontal pill, never a circle. Colour never stands alone: every lamp carries its status as a word, visibly or to assistive technology.",
    node: row(
      <Led key="up" status="up" hideLabel={false} />,
      <Led key="down" status="down" hideLabel={false} />,
      <Led key="pending" status="pending" hideLabel={false} />,
      <Led key="paused" status="paused" hideLabel={false} />,
      <Led key="stale" status="up" hideLabel={false} stale />,
    ),
  },
  {
    id: "chips",
    title: "Chips",
    source: "web/src/components/Chip.tsx",
    rule:
      "A filled chip states a status and is labelled in words. A dashed chip is a statement about the data — partial, stale, not verified — and is never drawn in a status colour, so it cannot be mistaken for one.",
    node: row(
      <StatusChip key="up" status="up">Up</StatusChip>,
      <StatusChip key="warn" status="warn">Slow</StatusChip>,
      <StatusChip key="down" status="down">Down</StatusChip>,
      <StatusChip key="idle" status="idle">Paused</StatusChip>,
      <StateChip key="state">Not verified</StateChip>,
      <CountChip key="count">12</CountChip>,
      <MetaChip key="meta" label="Every" value="60 s" />,
      <EmptyAvatar key="avatar" />,
    ),
  },
  {
    id: "value",
    title: "Value",
    source: "web/src/components/Value.tsx",
    rule:
      "A measurement. Tabular figures so a column lines up. A zero reading dims; an absent reading renders nothing, because nothing and zero are different statements. A caveat draws the dotted underline and is read aloud.",
    node: row(
      <Value key="a" value={142}>142 ms</Value>,
      <Value key="b" value={0}>0 ms</Value>,
      <Value key="c" value={null}>—</Value>,
      <Value key="d" value={99.98} warning="measured from one probe">
        99.98%
      </Value>,
    ),
  },
  {
    id: "buttons",
    title: "Buttons",
    source: "web/src/monitors/monitors.css, web/src/monitors/inventory.css",
    rule:
      "The accent fills controls and nothing else. A primary action gets the accent border, and carries a + when it adds the thing the surface it sits on is a list of. A compact row action sits in a 26px square with its name in aria-label. Delete is a glyph like its neighbours: the bin carries destructive in its shape, so it survives greyscale where red alone would not, and the confirmation that makes you type the name is what buys back the pause the word used to.",
    node: row(
      <button
        key="p"
        type="button"
        className="add-button add-button-primary"
        aria-label="Add monitor"
      >
        <PlusIcon aria-hidden="true" />
        Add monitor
      </button>,
      <button key="s" type="button" className="add-button">
        Cancel
      </button>,
      <button
        key="check"
        type="button"
        className="inv-act inv-act--icon"
        aria-label="Check now"
        title="Check now"
      >
        <IconRefresh />
      </button>,
      <button
        key="pause"
        type="button"
        className="inv-act inv-act--icon"
        aria-label="Pause"
        title="Pause"
      >
        <IconPause />
      </button>,
      <button
        key="edit"
        type="button"
        className="inv-act inv-act--icon"
        aria-label="Edit"
        title="Edit"
      >
        <IconPencil />
      </button>,
      <button
        key="del"
        type="button"
        className="inv-act inv-act--icon inv-act--danger"
        aria-label="Delete"
        title="Delete"
      >
        <IconTrash />
      </button>,
    ),
  },
  {
    id: "segmented",
    title: "Segmented control",
    source: "web/src/components/SegmentedControl.tsx",
    rule:
      "An 8px shell around 6px segments, concentric. The selected segment takes the accent. The group carries a label for assistive technology even when every segment is a word.",
    node: (
      <SegmentedControl
        label="Range"
        value="24h"
        onChange={() => {}}
        options={[
          { id: "1h", label: "1h" },
          { id: "24h", label: "24h" },
          { id: "7d", label: "7d" },
        ]}
      />
    ),
  },
  {
    id: "icontile",
    title: "Icon tile",
    source: "web/src/components/IconTile.tsx",
    rule: "The 24px tile beside a card title. Its border adds optical weight, which is why the gap to the title is a half-step wider than the grid would give.",
    node: row(
      <IconTile key="a">
        <IconRefresh />
      </IconTile>,
      <IconTile key="b">
        <IconPencil />
      </IconTile>,
    ),
  },
  {
    id: "chrome",
    title: "Masthead and toolbar",
    source: "web/src/shell/Topbar.tsx, web/src/shell/PageToolbar.tsx",
    rule:
      "Two bars, and which one a control belongs in is decided by a single question: is it true on every screen? The masthead holds what is — the sidebar toggle, search, the layout switcher, the theme. It never changes as you navigate, so it stays readable without being re-read. The toolbar below holds what is true of this screen only, and disappears on screens with nothing to put in it rather than sitting there empty. An action that operates on one kind of thing fails the question: adding a monitor is a monitors action, so it lives in the header of the card it adds to, not in chrome that is also present on Notifications.",
    node: (
      <div className="sg-chrome">
        <div className="shell-topbar">
          <button
            type="button"
            className="shell-topbar-icon"
            aria-label="Collapse sidebar (Ctrl+B)"
          >
            <SidebarIcon />
          </button>
          <label className="shell-search">
            <SearchIcon />
            <input
              type="search"
              className="shell-search-input"
              placeholder="Search monitors..."
              readOnly
            />
          </label>
        </div>
        <div className="shell-toolbar">
          <span className="tb-label">Type</span>
          <select className="tb-select" aria-label="Type">
            <option>All types</option>
          </select>
          <span className="tb-label">Paused</span>
          <select className="tb-select" aria-label="Paused">
            <option>All</option>
          </select>
          <span className="tb-count">4 of 4 shown</span>
        </div>
      </div>
    ),
  },
  {
    id: "card",
    title: "Card, panel, row",
    source: "web/src/components/Card.tsx, web/src/components/PanelList.tsx",
    rule:
      "Two surfaces, never three. A card frames a group; a panel row sits on it, and a panel is the quieter fill for a block that sits on a card. A row carries its status as a lamp plus a word, never as colour alone.",
    node: (
      <Card title="Checks" headingLevel={3}>
        <PanelList label="Recent checks">
          <PanelRow status="up" icon={<Led status="up" />}>
            api.example.com <Value value={88}>88 ms</Value>
          </PanelRow>
          <PanelRow status="down" icon={<Led status="down" />}>
            db.example.com <StatusChip status="down">Down</StatusChip>
          </PanelRow>
          <PanelRow status="paused" icon={<Led status="paused" />}>
            staging.example.com <StateChip>Paused</StateChip>
          </PanelRow>
        </PanelList>
        <Panel>
          <p className="text-helper text-ink-3">
            A panel: the quieter fill for a block that sits on a card.
          </p>
        </Panel>
      </Card>
    ),
  },
];
