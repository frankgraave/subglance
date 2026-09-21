import { expect, it } from "vitest";
import { parseEvent } from "../live/events";
import { applyHeartbeat } from "../live/apply";
import { toSlots } from "../heartbeat/model";
import { fromApi } from "./types";

it("preserves maintenance evidence through REST, SSE and grouped heartbeat columns",()=>{
 const monitor=fromApi({id:1,name:"service",type:"http",target:"https://example.invalid",interval_s:60,timeout_s:10,created_at:"2026-09-21T00:00:00Z",status:"up",enabled:true,maintenance:true,heartbeats:[{ts:"2026-09-21T00:00:00Z",ok:true,assessment:"up",maintenance:true}]});
 expect(monitor.maintenance).toBe(true);
 const event=parseEvent("heartbeat",JSON.stringify({monitor_id:1,at:"2026-09-21T00:01:00Z",data:{ok:false,assessment:"warning",maintenance:true,latency_ms:100}}));
 if(event?.kind!=="heartbeat")throw new Error("heartbeat not decoded");
 const [updated]=applyHeartbeat([monitor],event);
 const [slot]=toSlots(updated.beats,1);
 expect(slot).toMatchObject({count:2,maintenanceCount:2,warningCount:1});
});
