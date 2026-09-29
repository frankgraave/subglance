import { TokenSheet } from "./components/TokenSheet";
import { HeartbeatGallery } from "./heartbeat/Gallery";
import { DashboardWorkbench } from "./monitors/Workbench";

/**
 * The workbench's fixture galleries, in a module of their own so the bundler
 * can split them out of the entry chunk.
 *
 * Nothing here is reachable without pressing the workbench button: it is
 * demo monitors, every heartbeat state and the token sheet, drawn from
 * fixtures for judging components in isolation. Every visitor used to
 * download it on first load all the same, about 3 kB gzip of a bundle that
 * has a ceiling. App.tsx imports this file with `lazy()`, so the browser
 * fetches it the first time the workbench opens and never otherwise.
 *
 * A default export because `lazy()` resolves to the module's default.
 */
export default function WorkbenchGalleries() {
  return (
    <>
      <DashboardWorkbench />
      <HeartbeatGallery />
      <TokenSheet />
    </>
  );
}
