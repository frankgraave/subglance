import type { BrowserSession } from "./api";

// The browser the page runs in, a phone, and a client the server could not name.
export const sampleSessions: BrowserSession[] = [
  { id: "a".repeat(32), created_at: "2026-10-01T08:00:00Z", last_seen_at: "2026-10-07T09:00:00Z", expires_at: "2026-10-14T09:00:00Z",
    browser: "Firefox", platform: "macOS", user_agent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.6; rv:131.0) Gecko/20100101 Firefox/131.0",
    ip: "2001:db8::10", current: true },
  { id: "b".repeat(32), created_at: "2026-09-28T19:00:00Z", last_seen_at: "2026-10-06T21:00:00Z", expires_at: "2026-10-13T21:00:00Z",
    browser: "Safari", platform: "iPhone", user_agent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Version/18.0 Mobile/15E148 Safari/604.1",
    ip: "2001:db8::20", current: false },
  { id: "c".repeat(32), created_at: "2026-09-20T07:00:00Z", last_seen_at: "2026-09-30T07:00:00Z", expires_at: "2026-10-07T07:00:00Z",
    browser: "", platform: "", user_agent: "homegrown-client/2.1", ip: "2001:db8::30", current: false },
];
