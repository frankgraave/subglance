import { Card, Panel } from "../components/Card";
import { IconGlobe } from "../components/icons";
import { DASHBOARD_PATH } from "./route";

/**
 * The screen for an address that names no screen (SUB-177).
 *
 * The dashboard used to stand in for it, which made a mistyped or stale link
 * look as though it worked: the wrong address stayed in the bar under a tab
 * that said "Dashboard", so nobody corrected the link and nobody learned that
 * the page it promised was not there. This says so, quotes the address so the
 * typo can be seen, and offers the one place that always exists.
 *
 * A client-side screen inside the shell rather than a server 404: the server
 * hands the app to every path it does not serve, because the app's own routes
 * are paths too, and the rail beside this screen is the rest of the way out.
 */
export function NotFound({
  path,
  onGoToDashboard,
}: {
  path: string;
  /** Client-side navigation. Absent means the link does a full page load. */
  onGoToDashboard?: () => void;
}) {
  return (
    <Card title="Nothing at this address" icon={<IconGlobe />}>
      <Panel>
        <div className="stack">
          <p>
            SubGlance has no page at{" "}
            <code className="literal wrap-anywhere">{path}</code>. The link may be
            mistyped, or it may point to a page an earlier version had.
          </p>
          <div className="button-row">
            {/* A real link, for the reasons the rail's are: middle-click and
                copy-address work. The handler stands aside for anything but a
                plain left click. */}
            <a
              className="button button--primary"
              href={DASHBOARD_PATH}
              onClick={(event) => {
                if (
                  onGoToDashboard === undefined ||
                  event.defaultPrevented ||
                  event.button !== 0 ||
                  event.metaKey ||
                  event.ctrlKey ||
                  event.shiftKey ||
                  event.altKey
                ) {
                  return;
                }
                event.preventDefault();
                onGoToDashboard();
              }}
            >
              Go to the dashboard
            </a>
          </div>
        </div>
      </Panel>
    </Card>
  );
}
