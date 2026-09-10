/**
 * Visual proof that the tokens resolve in both themes.
 *
 * This is not decoration: a token file that is never rendered rots silently,
 * and a wrong contrast pair is invisible in a diff but obvious here. Every
 * swatch reads its colour through a Tailwind utility, so if a binding in the
 * `@theme inline` block is missing the cell renders unstyled and the mistake
 * is visible rather than theoretical.
 */

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mb-10">
      <h2 className="mb-3 text-[12px] font-medium uppercase tracking-[0.09em] text-ink-3">
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
      <code className="text-[12.5px] text-ink-2">{name}</code>
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
            <p key={i.name} className={`text-[13.5px] ${i.className}`}>
              {i.name}
            </p>
          ))}
        </div>
      </Section>

      <Section title="Status">
        {/* Colour never stands alone (DESIGN.md §2.3): each row carries the
            meaning as text as well, which is also how the real dashboard
            will do it. */}
        <ul className="rounded-lg border border-border bg-surface shadow-card">
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
              <code className="w-24 text-[12.5px] text-ink-2">{s.name}</code>
              <span className="text-[13px] text-ink-3">{s.label}</span>
            </li>
          ))}
        </ul>
      </Section>

      <Section title="Radius">
        <div className="flex gap-4">
          {["rounded-sm", "rounded-md", "rounded-lg"].map((r) => (
            <div
              key={r}
              className={`flex size-16 items-center justify-center border border-border-hi bg-surface-2 text-[11.5px] text-ink-3 ${r}`}
            >
              {r.replace("rounded-", "")}
            </div>
          ))}
        </div>
      </Section>

      <Section title="Type">
        <p className="text-[13.5px] text-ink">Row title — 13.5px, weight 450</p>
        <p className="font-mono text-[13px] text-ink-2">
          200 OK · 142 ms · 99.98% — mono, so columns line up
        </p>
      </Section>
    </div>
  );
}
