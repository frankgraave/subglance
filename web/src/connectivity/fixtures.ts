// Wire bodies for GET /api/v1/connectivity, for tests and the browser harness.
export const onlineConnectivity = {
  enabled: true,
  offline: false,
  offline_since: null,
  resolver_down: false,
  resolver_down_since: null,
  targets_unusable: null,
  targets_unusable_since: null,
};

export const offlineConnectivity = {
  ...onlineConnectivity,
  offline: true,
  offline_since: "2026-09-29T01:12:04Z",
};

export const resolverDownConnectivity = {
  ...onlineConnectivity,
  resolver_down: true,
  resolver_down_since: "2026-09-29T01:12:04Z",
};

// The targets have never answered: an internal network, or a firewall that
// drops outbound port 53. Nothing is held back.
export const unansweredConnectivity = {
  ...onlineConnectivity,
  targets_unusable: "unanswered",
  targets_unusable_since: "2026-10-18T03:34:43Z",
};

// The targets failed while another check reached a public address.
export const contradictedConnectivity = {
  ...onlineConnectivity,
  targets_unusable: "contradicted",
  targets_unusable_since: "2026-10-18T03:34:43Z",
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
