import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Drawer } from "../components/Drawer";
import { Panel } from "../components/Card";
import { EditMonitorForm } from "./EditMonitorForm";
import { fetchMonitorForEdit, patchMonitor } from "./inventoryApi";
import type { VersionedMonitor } from "./inventoryApi";
import { detailQueryKey } from "./detail";
import { openIncidentsQueryKey } from "../incidents/api";

/** Mounted per edit session. A reload replaces both values and validator;
 * canceled reads and writes cannot close or populate a later session. */
export function EditMonitorDrawer({ id, onClose }: { id: string; onClose: () => void }) {
  const client = useQueryClient();
  const [revision, setRevision] = useState(0);
  const [loaded, setLoaded] = useState<VersionedMonitor | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    void fetchMonitorForEdit(id, controller.signal).then((value) => {
      if (!controller.signal.aborted) setLoaded(value);
    }).catch((reason: unknown) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : "Could not load settings.");
    });
    return () => controller.abort();
  }, [id, revision]);
  const reload = () => { setLoaded(null); setError(null); setRevision((value) => value + 1); };
  // The save has its own lifetime, separate from the settings read.
  const saveController = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    saveController.current = controller;
    return () => controller.abort();
  }, []);
  return <Drawer open onClose={onClose} title={loaded ? `Edit ${loaded.monitor.name}` : "Edit monitor"}>
    <Panel>
      {error ? <><p role="alert" className="add-field-error">{error}</p><button type="button" className="add-button" onClick={reload}>Reload latest settings</button></>
        : loaded === null ? <p className="add-help">Loading current settings…</p>
        : <EditMonitorForm key={revision} monitor={loaded.monitor} onReload={reload} onCancel={onClose} onSave={async (patch) => {
          const controller = saveController.current!;
          await patchMonitor(id, patch, loaded.etag, controller.signal);
          if (controller.signal.aborted) return;
          await Promise.all([
            client.invalidateQueries({ queryKey: ["monitors"] }),
            client.invalidateQueries({ queryKey: detailQueryKey(id) }),
            client.invalidateQueries({ queryKey: openIncidentsQueryKey }),
          ]);
          if (!controller.signal.aborted) onClose();
        }} />}
    </Panel>
  </Drawer>;
}
