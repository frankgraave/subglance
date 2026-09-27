// Diagnostic fixture only: no target, so no invented bucket or backup history.
export const unconfiguredBackup = {
  configured: false, target: null,
  last_success_at: null, last_object: null, last_size_bytes: null,
  last_error: null, last_error_at: null, failures: 0,
};
