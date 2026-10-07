/**
 * The address of one incident: `/incidents#incident-312`.
 *
 * An alert links here when the instance knows its own address (`--base-url`),
 * so the person it woke lands on the incident rather than on a list to search.
 * A fragment rather than a route of its own, because an incident has no page:
 * it is a row that expands in place, and the list around it is part of what
 * the reader came to see. The server builds the same shape in
 * `internal/notifier/links.go`; the two are one contract.
 */
export function incidentAnchor(id: string): string {
  return `incident-${id}`;
}

/** Whether the address names this incident, so its row opens on arrival. */
export function isLinkedIncident(id: string): boolean {
  return window.location.hash === `#${incidentAnchor(id)}`;
}
