/** Retry all known schedules even when discovery or one deletion fails. */
export function maintenanceCleanup(url: string, session: string, request: typeof fetch = fetch) {
  const pending = new Set<number>();
  const headers = { Cookie: `subglance_session=${session}` };
  return async () => {
    const errors: string[] = [];
    try {
      const response = await request(`${url}/api/v1/maintenance`, { headers });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const data = await response.json() as { maintenance: { id: number }[] };
      for (const window of data.maintenance) pending.add(window.id);
    } catch (error) { errors.push(`list maintenance: ${String(error)}`); }
    for (const id of pending) {
      try {
        const response = await request(`${url}/api/v1/maintenance/${id}`, { method: "DELETE", headers });
        if (!response.ok && response.status !== 404) throw new Error(`HTTP ${response.status}`);
        pending.delete(id);
      } catch (error) { errors.push(`delete maintenance ${id}: ${String(error)}`); }
    }
    return errors;
  };
}
