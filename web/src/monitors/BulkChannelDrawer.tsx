import { useRef } from "react";
import { Drawer } from "../components/Drawer";
import { Panel } from "../components/Card";
import type { ChannelChange } from "./bulkChannelsApi";
import { LazyBulkChannelForm } from "./LazyMonitorForms";

/**
 * The selection's channels, in a drawer over the inventory like Manage tags.
 * The form loads on first use; the drawer, its title and its close button are
 * on screen at once. Escape and the close button wait while a request runs,
 * so a confirm in flight cannot land behind a closed drawer.
 */
export function BulkChannelDrawer({ selectedIds, onDefault, onChange, onClose }: {
  selectedIds: readonly string[];
  onDefault: number;
  onChange: ChannelChange;
  onClose: () => void;
}) {
  const busy = useRef(false);
  return (
    <Drawer open title="Channels for selected monitors" className="bulk-channels-drawer"
      onClose={() => { if (!busy.current) onClose(); }}>
      <Panel>
        <LazyBulkChannelForm selectedIds={selectedIds} onDefault={onDefault} onChange={onChange}
          onClose={onClose} onBusyChange={(next) => { busy.current = next; }} />
      </Panel>
    </Drawer>
  );
}
