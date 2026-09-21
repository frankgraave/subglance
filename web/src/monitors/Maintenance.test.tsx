// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorsRoot } from "./LiveMonitors";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("makes maintenance discoverable to a viewer without write controls", async () => {
 vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response(JSON.stringify({maintenance:[]})));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} canWrite={false}/>);
 const disclosure=await screen.findByText("Manage scheduled maintenance");
 fireEvent.click(disclosure);
 await waitFor(()=>expect(screen.getByText("No maintenance windows scheduled.")).toBeTruthy());
 expect(screen.queryByRole("button",{name:"Schedule maintenance"})).toBeNull();
});

it("reports an unavailable schedule read without claiming there are no windows", async () => {
 vi.spyOn(globalThis,"fetch").mockImplementation(async()=>new Response(JSON.stringify({error:"database unavailable"}),{status:500}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} canWrite={false}/>);
 fireEvent.click(await screen.findByText("Manage scheduled maintenance"));
 expect((await screen.findByRole("alert")).textContent).toContain("database unavailable");
 expect(screen.queryByText("No maintenance windows scheduled.")).toBeNull();
});


it("invalidates cached reminder reads after cancelling maintenance", async () => {
 const client=new QueryClient({defaultOptions:{queries:{retry:false,staleTime:Infinity}}});
 client.setQueryData(["monitor-detail","1"],{incidents:[{reminder:{status:"maintenance"}}]});
 client.setQueryData(["incidents","open"],[{reminder:{status:"maintenance"}}]);
 let cancelled=false;
 vi.spyOn(globalThis,"fetch").mockImplementation(async(_url,init)=>{
  if(init?.method==="DELETE"){cancelled=true;return new Response(null,{status:204});}
  return Response.json({maintenance:cancelled?[]:[{id:1,name:"Deploy",monitor_id:1,active:true}]});
 });
 render(<LiveMonitorsRoot client={client} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByText("Manage scheduled maintenance"));
 fireEvent.click(await screen.findByRole("button",{name:"Cancel maintenance Deploy"}));
 await screen.findByText("Cancelled Deploy. Recorded history is unchanged.");
 expect(client.getQueryState(["monitor-detail","1"])?.isInvalidated).toBe(true);
 expect(client.getQueryState(["incidents","open"])?.isInvalidated).toBe(true);
 client.clear();
});
