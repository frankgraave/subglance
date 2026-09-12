import { useEffect, useState } from "react";
import { HeartbeatBar } from "./HeartbeatBar";
import type { Beat } from "./model";

/**
 * A storybook without Storybook.
 *
 * The component's acceptance criterion is that it can be judged in isolation,
 * which needs every hard case visible side by side — not a dependency that
 * doubles the frontend toolchain. Each case below is one that broke a naive
 * implementation: an outlier that flattens the trend, a bucket hiding a
 * failure, a series too short to fill the track, no data at all.
 */

const NOW = Date.now();

function series(count: number, shape: (i: number) => Partial<Beat>): Beat[] {
  return Array.from({ length: count }, (_, i) => ({
    ts: NOW - (count - 1 - i) * 60_000,
    ok: true,
    latencyMs: 120,
    ...shape(i),
  }));
}

const wobble = (i: number, base: number, spread: number) =>
  Math.round(base + Math.sin(i * 0.7) * spread + (i % 5) * (spread / 4));

const CASES: { title: string; note: string; beats: Beat[] }[] = [
  {
    title: "Healthy, 28 checks",
    note: "The everyday case. Height varies with latency, so a creeping slowdown is visible before anything fails.",
    beats: series(28, (i) => ({ latencyMs: wobble(i, 90, 25) })),
  },
  {
    title: "Degrading",
    note: "Latency climbing into failures. Colour and height carry the story twice over.",
    beats: series(28, (i) => ({
      latencyMs: 60 + i * 45,
      ok: i < 24,
      error: i >= 24 ? "context deadline exceeded" : undefined,
    })),
  },
  {
    title: "One outlier among 40",
    note: "A single 9-second probe. The scale is the 95th percentile, so the outlier clamps instead of squashing every other bar into a stub.",
    beats: series(40, (i) => ({ latencyMs: i === 30 ? 9000 : wobble(i, 110, 30) })),
  },
  {
    title: "500 checks in the same width",
    note: "Bucketed to fit. A bucket containing one failure is drawn as failed — worst wins, never the average, or the graphic would hide the one thing it exists to show.",
    beats: series(500, (i) => ({
      latencyMs: wobble(i, 130, 40),
      ok: !(i > 300 && i < 306),
      error: i > 300 && i < 306 ? "502 Bad Gateway" : undefined,
    })),
  },
  {
    title: "Only 6 checks",
    note: "A monitor added minutes ago. The bars keep their width and the missing history stays visibly empty; stretching them would claim data we do not have.",
    beats: series(6, (i) => ({ latencyMs: wobble(i, 80, 20) })),
  },
  {
    title: "No timing recorded",
    note: "Checks that passed without a measurable latency. Drawn amber at a fixed middle height: absence and zero are different facts.",
    beats: series(20, () => ({ latencyMs: null })),
  },
  {
    title: "No data at all",
    note: "A paused or brand-new monitor. An idle track, not a blank space, so it reads as 'nothing yet' rather than a broken render.",
    beats: [],
  },
];

/** Appends a check every 3 seconds so the entry animation can be judged live. */
function useLiveSeries(): Beat[] {
  const [beats, setBeats] = useState(() => series(28, (i) => ({ latencyMs: wobble(i, 100, 30) })));
  useEffect(() => {
    const timer = setInterval(() => {
      setBeats((current) => {
        const ok = Math.random() > 0.18;
        return [
          ...current.slice(1),
          {
            ts: Date.now(),
            ok,
            latencyMs: ok ? 70 + Math.round(Math.random() * 160) : 30,
            error: ok ? undefined : "connection refused",
          },
        ];
      });
    }, 3000);
    return () => clearInterval(timer);
  }, []);
  return beats;
}

function Case({ title, note, beats }: { title: string; note: string; beats: Beat[] }) {
  return (
    <section className="rounded-lg border border-border bg-surface p-5 shadow-card">
      <h3 className="text-card font-strong text-ink">{title}</h3>
      <p className="mb-4 mt-1 max-w-prose text-helper leading-[var(--lh-prose)] text-ink-3">{note}</p>
      <HeartbeatBar beats={beats} label={title} />
    </section>
  );
}

export function HeartbeatGallery() {
  const live = useLiveSeries();
  return (
    <div className="grid gap-5">
      <section className="rounded-lg border border-border bg-surface p-5 shadow-card">
        <h3 className="text-card font-strong text-ink">Live — a check every 3 seconds</h3>
        <p className="mb-4 mt-1 max-w-prose text-helper leading-[var(--lh-prose)] text-ink-3">
          The newest bar enters at 35% height, overshoots and settles (DESIGN.md §5). The row itself
          stays still; nothing flashes. Hover or focus the track and use the arrow keys.
        </p>
        <HeartbeatBar beats={live} label="Live demo" />
      </section>
      {CASES.map((c) => (
        <Case key={c.title} {...c} />
      ))}
      <section className="rounded-lg border border-border bg-surface p-5 shadow-card">
        <h3 className="text-card font-strong text-ink">Sizes</h3>
        <p className="mb-4 mt-1 max-w-prose text-helper leading-[var(--lh-prose)] text-ink-3">
          The same series at row height, card height and hero height. The column count follows the
          available width, so the component never needs to be told how much data to show.
        </p>
        <div className="grid gap-4">
          {[
            { height: 18, barWidth: 3, gap: 2 },
            { height: 34, barWidth: 6, gap: 3 },
            { height: 64, barWidth: 10, gap: 4 },
          ].map((size) => (
            <HeartbeatBar
              key={size.height}
              beats={series(120, (i) => ({ latencyMs: wobble(i, 120, 40), ok: i !== 96 }))}
              label={`Sizes, ${size.height}px`}
              {...size}
            />
          ))}
        </div>
      </section>
    </div>
  );
}
