/**
 * The demo estate `make seed` creates, read out of `cmd/seed/catalogue.go`.
 *
 * Shared by the browser tests that measure the seed's names on screen
 * (SUB-194), so a longer name added to the seed later is measured too, and
 * so they all parse the catalogue the same way. A field the parser cannot
 * find throws rather than defaulting: a reshaped catalogue is noticed instead
 * of silently measuring nothing.
 */
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import type { ApiMonitor } from "../../monitors/types";

const repoRoot = fileURLToPath(new URL("../../../../", import.meta.url));

/** The Go type expressions the catalogue uses, as the wire's type names. */
const SEED_TYPES: Record<string, string> = {
  "string(checker.TypeHTTP)": "http",
  "string(checker.TypeTCP)": "tcp",
  "string(checker.TypePing)": "ping",
  "string(checker.TypeSSL)": "ssl",
  "store.TypePush": "push",
};

/**
 * The seed's monitors, as the list endpoint would return them.
 *
 * Only what the row draws is read: name, type, target, interval, timeout,
 * whether it is paused, its tags and a push monitor's interval. A field this
 * parser cannot find fails the test rather than defaulting, so a reshaped
 * catalogue is noticed instead of silently measuring nothing.
 */
export function seedEstate(): ApiMonitor[] {
  const source = catalogue();
  const body = source.slice(source.indexOf("func monitors() []monitorSpec"));
  const specs = body.split("monitor: store.Monitor{").slice(1);
  return specs.map((spec, i) => {
    const block = spec.slice(0, spec.indexOf("\n\t\t\t},"));
    const read = (pattern: RegExp, what: string): string => {
      const found = pattern.exec(block);
      if (!found) throw new Error(`seed monitor ${i + 1}: no ${what} in ${block.slice(0, 80)}`);
      return found[1]!;
    };
    const type = SEED_TYPES[read(/Type: ([^,]+),/, "type")];
    if (type === undefined) throw new Error(`seed monitor ${i + 1}: unknown type`);
    const tags = Object.fromEntries(
      [...(/Tags:\s+map\[string\]string\{([^}]*)\}/.exec(block)?.[1] ?? "").matchAll(/"([^"]+)": "([^"]+)"/g)].map(
        (pair) => [pair[1]!, pair[2]!],
      ),
    );
    const push = /PushIntervalS: (\d+)/.exec(block);
    return {
      id: i + 1,
      name: read(/Name: "([^"]*)"/, "name"),
      type,
      target: read(/Target:\s+"([^"]*)"/, "target"),
      interval_s: Number(read(/IntervalS: (\d+)/, "interval")),
      timeout_s: Number(read(/TimeoutS: (\d+)/, "timeout")),
      enabled: !/Enabled: false/.test(block),
      status: "up",
      created_at: new Date().toISOString(),
      tags,
      channels: [{ id: 1, name: "Ops Slack" }],
      rule_channels: [],
      ...(push ? { push_interval_s: Number(push[1]), push_grace_s: 300, push_token_prefix: "sgp_seed" } : {}),
    } as ApiMonitor;
  });
}


function catalogue(): string {
  return readFileSync(join(repoRoot, "cmd/seed/catalogue.go"), "utf8");
}

/**
 * The seed's failure texts, keyed by the failure kind the seed files them
 * under: the `err*` constants of the catalogue, which are the strings the
 * checker produces.
 *
 * The constant-to-kind table is the one thing written here rather than read,
 * because the catalogue pairs them per outage. A constant that is not in the
 * table throws, so a new kind of seeded failure is added here on purpose.
 */
export function seedFailures(): { cause: string; message: string }[] {
  const kinds: Record<string, string> = {
    errRefused: "connection",
    errTimeout: "timeout",
    errDNS: "dns",
    errStatus5: "status",
    errStatus4: "status",
    errKeyword: "keyword",
    errCert: "cert_expiry",
    errOverdue: "push_overdue",
    errReported: "push_reported",
  };
  const source = catalogue();
  const block = /\nconst \(\n([\s\S]*?)\n\)/.exec(source.slice(source.indexOf("// A failing check's error text")));
  if (!block) throw new Error("seed catalogue: no error-text constants");
  return [...block[1]!.matchAll(/^\s*(err\w+)\s*=\s*(?:"([^"]*)"|`([^`]*)`)/gm)].map((found) => {
    const cause = kinds[found[1]!];
    if (cause === undefined) throw new Error(`seed catalogue: ${found[1]} has no failure kind in seedFailures()`);
    return { cause, message: found[2] ?? found[3]! };
  });
}
