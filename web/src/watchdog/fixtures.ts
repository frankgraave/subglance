// Diagnostic fixture only. No configured receiver or invented ping history.
export const disabledWatchdog = {
  configured: false, interval_seconds: null,
  last_decision_at: null, last_attempt_at: null, last_success_at: null,
  last_result: null, last_status_code: null, last_event: null,
  suppressed: false, in_flight: false, overdue: false,
};
