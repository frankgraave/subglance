import { createContext } from "react";

/**
 * True under a screen that owns its data and can ask where an incident's
 * alerts went.
 *
 * A context rather than a prop because the row that draws the list sits three
 * components below the screen that knows (the incidents view, a cluster, the
 * row), and every one of them is also rendered on its own, in tests and the
 * workbench, with no query client above it. Outside a provider the list is
 * simply not offered, which is what a fixture with no server should do.
 */
export const IncidentDeliveriesScope = createContext(false);
