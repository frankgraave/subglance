import { expect, it } from "vitest";
import { windowFromApi } from "./detail";

it.each([undefined, Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY])(
  "rejects non-finite exclusion counters: %s", (value) => {
    expect(windowFromApi({ window: "24h", window_s: 86400, total: 0, up: 0, down: 0,
      uptime: null, warning: value, maintenance: value, legacy: value,
    })).toMatchObject({ warning: 0, maintenance: 0, legacy: 0 });
  },
);
