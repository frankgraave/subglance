// Wire bodies for GET /api/v1/connectivity, for tests and the browser harness.
export const onlineConnectivity = { enabled: true, offline: false, offline_since: null };

export const offlineConnectivity = {
  enabled: true,
  offline: true,
  offline_since: "2026-09-29T01:12:04Z",
};

// Wire bodies for GET /api/v1/settings/connectivity (SUB-168): the defaults,
// and a deployment that fixed both settings with environment variables.
export const defaultConnectivitySettings = {
  enabled: { value: true, source: "default", pinned_by: null },
  targets: { value: ["1.1.1.1:53", "9.9.9.9:53"], source: "default", pinned_by: null },
  default_targets: ["1.1.1.1:53", "9.9.9.9:53"],
  max_targets: 16,
};

export const pinnedConnectivitySettings = {
  ...defaultConnectivitySettings,
  enabled: { value: true, source: "pinned", pinned_by: "SUBGLANCE_CONNECTIVITY_CHECK" },
  targets: { value: ["gateway:443", "backup-host:22"], source: "pinned", pinned_by: "SUBGLANCE_CONNECTIVITY_TARGETS" },
};
