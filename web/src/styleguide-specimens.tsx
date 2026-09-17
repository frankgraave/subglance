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
import { IconPause, IconPencil, IconRefresh } from "./components/icons";
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
      "The accent fills controls and nothing else. A primary action gets the accent border; a compact icon action sits in a 26px square with its name in aria-label. Delete stays a word, because colour may not be the only carrier of destructive.",
    node: row(
      <button key="p" type="button" className="add-button add-button-primary">
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
      <button key="del" type="button" className="inv-act inv-act--danger">
        Delete
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
