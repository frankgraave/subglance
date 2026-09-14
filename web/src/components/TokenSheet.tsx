/**
 * Visual proof that the tokens resolve in both themes.
 *
 * This is not decoration: a token file that is never rendered rots silently,
 * and a wrong contrast pair is invisible in a diff but obvious here. Every
 * swatch reads its colour through a Tailwind utility, so if a binding in the
 * `@theme inline` block is missing the cell renders unstyled and the mistake
 * is visible rather than theoretical.
 */

import {
  CountChip,
  EmptyAvatar,
  IconTile,
  MetaChip,
  StateChip,
  StatusChip,
} from "./Chip";
import { Tooltip } from "./Tooltip";

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mb-10">
      <h2 className="mb-3 caps-legend">
        {title}
      </h2>
      {children}
    </section>
  );
}

function Swatch({ name, className }: { name: string; className: string }) {
  return (
    <div className="flex items-center gap-3">
      <div className={`size-9 rounded-md border border-border ${className}`} />
      <code className="text-helper text-ink-2">{name}</code>
    </div>
  );
}

const SURFACES = [
  { name: "--canvas", className: "bg-canvas" },
  { name: "--surface", className: "bg-surface" },
  { name: "--surface-2", className: "bg-surface-2" },
  { name: "--surface-hi", className: "bg-surface-hi" },
  { name: "--border", className: "bg-border" },
  { name: "--border-hi", className: "bg-border-hi" },
];

const INK = [
  { name: "--ink", className: "text-ink" },
  { name: "--ink-2", className: "text-ink-2" },
  { name: "--ink-3", className: "text-ink-3" },
  { name: "--ink-4", className: "text-ink-4" },
];

const STATUS = [
  { name: "--up", dot: "bg-up", glow: "var(--glow-up)", label: "Last check passed" },
  { name: "--warn", dot: "bg-warn", glow: "var(--glow-warn)", label: "Slow, or cert expiring" },
  { name: "--down", dot: "bg-down", glow: "var(--glow-down)", label: "Last check failed" },
  { name: "--idle", dot: "bg-idle", glow: "var(--glow-idle)", label: "Paused, or no data yet" },
];

export function TokenSheet() {
  return (
    <div>
      <Section title="Surfaces">
        <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
          {SURFACES.map((s) => (
            <Swatch key={s.name} name={s.name} className={s.className} />
          ))}
        </div>
      </Section>

      <Section title="Ink">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {INK.map((i) => (
            <p key={i.name} className={`text-row ${i.className}`}>
              {i.name}
            </p>
          ))}
        </div>
      </Section>

      <Section title="Status">
        {/* Colour never stands alone (DESIGN.md §2.3): each row carries the
            meaning as text as well, which is also how the real dashboard
            will do it. */}
        <ul className="rounded-lg border border-border bg-surface shadow-flat">
          {STATUS.map((s, index) => (
            <li
              key={s.name}
              className={`flex items-center gap-3 px-4 py-3 ${
                index > 0 ? "border-t border-border" : ""
              }`}
            >
              <span
                className={`size-2.5 shrink-0 rounded-full ${s.dot}`}
                style={{ boxShadow: s.glow }}
              />
              <code className="w-24 text-helper text-ink-2">{s.name}</code>
              <span className="text-body text-ink-3">{s.label}</span>
            </li>
          ))}
        </ul>
      </Section>

      <Section title="Radius">
        <div className="flex gap-4">
          {["rounded-sm", "rounded-md", "rounded-lg"].map((r) => (
            <div
              key={r}
              className={`flex size-16 items-center justify-center border border-border-hi bg-surface-2 text-helper text-ink-3 ${r}`}
            >
              {r.replace("rounded-", "")}
            </div>
          ))}
        </div>
      </Section>

      {/* The five chip kinds together, because the rule that holds them apart
          (DESIGN.md §8.1 — dashed means "about the data", solid means "is the
          data") is only judgeable side by side. Rendering them here is also
          what lets the browser test measure a real fill against its label. */}
      <Section title="Chips">
        <div className="flex flex-wrap items-center gap-3">
          <StatusChip status="up">Up</StatusChip>
          <StatusChip status="warn">Slow</StatusChip>
          <StatusChip status="down">Down</StatusChip>
          <StatusChip status="idle">Paused</StatusChip>
          <CountChip>12</CountChip>
          <MetaChip label="Region" value="eu-west" />
          <StateChip>Partial data</StateChip>
          <EmptyAvatar />
          <IconTile>@</IconTile>
        </div>
      </Section>

      {/* The tooltip out of its hover, so its structure can be judged and
          measured: the divider running the full width, the value column
          aligned right, and the dashed chip that says the window is
          incomplete (DESIGN.md §8.3). */}
      <Section title="Tooltip">
        <div className="flex flex-wrap gap-4">
          <div className="hb-tooltip" style={{ position: "static" }}>
            <Tooltip
              timestamp="14 Sep 09:00 – 09:20"
              unit="ms"
              rows={[
                { key: "slowest", label: "Slowest", value: "142", marker: "up", status: "Up" },
                { key: "median", label: "Median", value: "96", marker: "up", status: "Up" },
              ]}
              total={{ label: "Total", value: "20 checks" }}
            />
          </div>
          <div className="hb-tooltip" style={{ position: "static" }}>
            <Tooltip
              timestamp="14 Sep 03:00 – 03:20"
              unit="ms"
              rows={[
                { key: "slowest", label: "Slowest", value: "98", marker: "up", status: "Up" },
              ]}
              total={{ label: "Total", value: "2 checks" }}
              partial
            />
          </div>
        </div>
      </Section>

      <Section title="Type">
        <p className="text-row text-ink">Row title — --type-row, weight 450</p>
        <p className="face-mono text-body text-ink-2">
          200 OK · 142 ms · 99.98% — mono, so columns line up
        </p>
      </Section>
    </div>
  );
}
