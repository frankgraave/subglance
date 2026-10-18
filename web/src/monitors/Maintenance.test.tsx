// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorsRoot } from "./LiveMonitors";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("makes maintenance discoverable to a viewer without write controls", async () => {
 vi.spyOn(globalThis,"fetch").mockResolvedValue(new Response(JSON.stringify({maintenance:[]})));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} canWrite={false}/>);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 await waitFor(()=>expect(screen.getByText("No maintenance windows scheduled.")).toBeTruthy());
 expect(screen.queryByRole("button",{name:"Schedule maintenance"})).toBeNull();
});

it("reports an unavailable schedule read without claiming there are no windows", async () => {
 vi.spyOn(globalThis,"fetch").mockImplementation(async()=>new Response(JSON.stringify({error:"database unavailable"}),{status:500}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} canWrite={false}/>);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 expect((await screen.findByRole("alert")).textContent).toContain("database unavailable");
 expect(screen.queryByText("No maintenance windows scheduled.")).toBeNull();
 expect(screen.getByRole("alert").textContent).not.toContain("out of date");
});

it("keeps the loaded schedules when a refresh fails, and says they may be stale", async () => {
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 let failing=false;
 vi.spyOn(globalThis,"fetch").mockImplementation(async()=>failing
  ? new Response(JSON.stringify({error:"database unavailable"}),{status:500})
  : Response.json({maintenance:[{id:1,name:"Deploy",monitor_id:1,active:false}]}));
 render(<LiveMonitorsRoot client={client} fetchMonitors={async()=>[]} canWrite={false}/>);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 await screen.findByText("Deploy");
 failing=true;
 await act(async()=>{ await client.refetchQueries({queryKey:["maintenance"]}); });
 expect((await screen.findByRole("alert")).textContent).toBe("Could not refresh maintenance: database unavailable. Showing the last loaded schedules, which may be out of date.");
 expect(screen.getByText("Deploy")).toBeTruthy();
 client.clear();
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
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 fireEvent.click(await screen.findByRole("button",{name:"Cancel maintenance Deploy"}));
 await screen.findByText("Cancelled Deploy. Recorded history is unchanged.");
 expect(client.getQueryState(["monitor-detail","1"])?.isInvalidated).toBe(true);
 expect(client.getQueryState(["incidents","open"])?.isInvalidated).toBe(true);
 client.clear();
});

it("puts a weekly duration out of range under the Duration field, not in the form-wide alert", async () => {
 const fetchSpy=vi.spyOn(globalThis,"fetch").mockResolvedValue(Response.json({maintenance:[]}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 fireEvent.change(await screen.findByLabelText("Schedule"),{target:{value:"weekly"}});
 const box=screen.getByLabelText("Duration");
 expect(box.getAttribute("aria-invalid")).toBeNull();
 fireEvent.change(box,{target:{value:"0"}});
 const posts=()=>fetchSpy.mock.calls.filter(([,init])=>init?.method==="POST").length;
 fireEvent.submit(screen.getByRole("form",{name:"Schedule maintenance"}));
 // The words sit in their own span beside the glyph; the refusal is the paragraph around them.
 const message=(await screen.findByText("The duration must be between 1 min and 24 h, in whole minutes.")).closest("p")!;
 expect(box.getAttribute("aria-invalid")).toBe("true");
 expect(box.getAttribute("aria-describedby")).toBe(message.id);
 expect(message.className).toBe("field-error");
 expect(message.querySelector("svg")).not.toBeNull();
 expect(message.closest(".field")).toBe(box.closest(".field"));
 expect(posts()).toBe(0);
 fireEvent.change(box,{target:{value:"30"}});
 expect(screen.queryByText("The duration must be between 1 min and 24 h, in whole minutes.")).toBeNull();
 expect(box.getAttribute("aria-invalid")).toBeNull();
});

/** Opens the schedule with a server that answers every POST with `refusal`. */
async function openRefusing(refusal: { error: string; field?: string }) {
 vi.spyOn(globalThis,"fetch").mockImplementation(async(_url,init)=>init?.method==="POST"
  ? Response.json(refusal,{status:400})
  : Response.json({maintenance:[]}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 return screen.findByRole("form",{name:"Schedule maintenance"});
}

it("puts a server refusal that names the timezone under the Timezone field", async () => {
 const form=await openRefusing({error:"unknown IANA timezone",field:"timezone"});
 fireEvent.change(screen.getByLabelText("Schedule"),{target:{value:"weekly"}});
 const box=screen.getByLabelText("Timezone");
 const help=box.getAttribute("aria-describedby");
 expect(help).not.toBeNull();
 fireEvent.submit(form);
 const message=(await screen.findByText("unknown IANA timezone")).closest("p")!;
 expect(message.className).toBe("field-error");
 expect(message.closest(".field")).toBe(box.closest(".field"));
 expect(box.getAttribute("aria-invalid")).toBe("true");
 // The refusal is read first, and the timezone help is still read after it.
 expect(box.getAttribute("aria-describedby")).toBe(`${message.id} ${help}`);
 // Said once, beside the field, not again in the form-wide alert.
 expect(screen.getAllByRole("alert")).toEqual([message]);
 await waitFor(()=>expect(document.activeElement).toBe(box));
 // Changing the field may be the fix, so the refusal goes with the change.
 fireEvent.change(box,{target:{value:"Europe/Amsterdam"}});
 expect(screen.queryByText("unknown IANA timezone")).toBeNull();
 expect(box.getAttribute("aria-invalid")).toBeNull();
 expect(box.getAttribute("aria-describedby")).toBe(help);
});

it("puts a refused tag value under the Tag value field", async () => {
 const form=await openRefusing({error:"tag value must not contain a comma",field:"tag_value"});
 fireEvent.change(screen.getByLabelText("Applies to"),{target:{value:"tag"}});
 fireEvent.submit(form);
 const message=(await screen.findByText("tag value must not contain a comma")).closest("p")!;
 const box=screen.getByLabelText("Tag value");
 expect(message.closest(".field")).toBe(box.closest(".field"));
 expect(box.getAttribute("aria-invalid")).toBe("true");
 expect(screen.getByLabelText("Tag key").getAttribute("aria-invalid")).toBeNull();
});

it("keeps a refusal with no field, or one naming a field the form does not show, in the form-wide alert", async () => {
 const form=await openRefusing({error:"choose exactly one monitor or tag pair"});
 fireEvent.submit(form);
 const alert=await screen.findByRole("alert");
 expect(alert.textContent).toBe("choose exactly one monitor or tag pair");
 expect(alert.className).not.toBe("field-error");
 expect(form.querySelector('[aria-invalid="true"]')).toBeNull();
 cleanup(); vi.restoreAllMocks();
 // The form is one-off, so it draws no Timezone field to stand under.
 const oneOff=await openRefusing({error:"recurrence requires timezone",field:"timezone"});
 fireEvent.submit(oneOff);
 expect((await screen.findByRole("alert")).textContent).toBe("recurrence requires timezone");
 expect(oneOff.querySelector('[aria-invalid="true"]')).toBeNull();
});

/*
 * The one-off form and the list, on a reader's clock. Pinned to Amsterdam so
 * the gap from UTC is visible: 20:00 there is 18:00 UTC in October.
 */
async function inAmsterdam(run: () => Promise<void>) {
 const zone=process.env.TZ;
 process.env.TZ="Europe/Amsterdam";
 try { await run(); }
 finally { if(zone===undefined) delete process.env.TZ; else process.env.TZ=zone; }
}

it("sends a one-off window typed at 20:00 in Amsterdam as 18:00 UTC, and says which zone it read", () => inAmsterdam(async () => {
 const posted:unknown[]=[];
 vi.spyOn(globalThis,"fetch").mockImplementation(async(_url,init)=>{
  if(init?.method==="POST"){posted.push(JSON.parse(String(init.body)));return Response.json({id:9},{status:201});}
  return Response.json({maintenance:[]});
 });
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 const form=await screen.findByRole("form",{name:"Schedule maintenance"});
 const start=screen.getByLabelText("Start");
 expect(document.getElementById(start.getAttribute("aria-describedby")!)?.textContent).toContain("Times are in your time zone (Europe/Amsterdam).");
 expect(screen.queryByText(/\(UTC\)/)).toBeNull();
 fireEvent.change(screen.getByLabelText("Name"),{target:{value:"Deploy"}});
 fireEvent.change(screen.getByLabelText("Applies to"),{target:{value:"tag"}});
 fireEvent.change(screen.getByLabelText("Tag key"),{target:{value:"env"}});
 fireEvent.change(screen.getByLabelText("Tag value"),{target:{value:"prod"}});
 fireEvent.change(start,{target:{value:"2026-10-20T20:00"}});
 fireEvent.change(screen.getByLabelText("End"),{target:{value:"2026-10-20T21:00"}});
 fireEvent.submit(form);
 await screen.findByText("Maintenance scheduled.");
 expect(posted).toEqual([{name:"Deploy",tag_key:"env",tag_value:"prod",starts_at:"2026-10-20T18:00:00.000Z",ends_at:"2026-10-20T19:00:00.000Z"}]);
}));

it("refuses a start the clocks skip under the Start field, without sending it", () => inAmsterdam(async () => {
 const fetchSpy=vi.spyOn(globalThis,"fetch").mockResolvedValue(Response.json({maintenance:[]}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 const form=await screen.findByRole("form",{name:"Schedule maintenance"});
 const start=screen.getByLabelText("Start");
 fireEvent.change(start,{target:{value:"2026-03-29T02:30"}});
 fireEvent.change(screen.getByLabelText("End"),{target:{value:"2026-03-29T04:00"}});
 fireEvent.submit(form);
 const message=(await screen.findByText(/02:30 does not happen on that day/)).closest("p")!;
 expect(message.closest(".field")).toBe(start.closest(".field"));
 expect(start.getAttribute("aria-invalid")).toBe("true");
 await waitFor(()=>expect(document.activeElement).toBe(start));
 expect(fetchSpy.mock.calls.filter(([,init])=>init?.method==="POST")).toEqual([]);
}));

it("fills the weekly timezone with the browser's zone", () => inAmsterdam(async () => {
 vi.spyOn(globalThis,"fetch").mockResolvedValue(Response.json({maintenance:[]}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 fireEvent.change(await screen.findByLabelText("Schedule"),{target:{value:"weekly"}});
 expect((screen.getByLabelText("Timezone") as HTMLInputElement).value).toBe("Europe/Amsterdam");
}));

const LISTED=[
 {id:1,name:"Last week's deploy",monitor_id:1,starts_at:"2026-10-01T18:00:00Z",ends_at:"2026-10-01T19:00:00Z",active:false},
 {id:2,name:"Patch night",tag_key:"env",tag_value:"prod",timezone:"UTC",weekdays:[0],local_time:"02:00",duration_minutes:60,active:false},
 {id:3,name:"Old migration",monitor_id:1,starts_at:"2026-09-01T18:00:00Z",ends_at:"2026-09-02T19:00:00Z",active:false},
 {id:4,name:"Next deploy",monitor_id:1,starts_at:"2099-01-01T18:00:00Z",ends_at:"2099-01-01T19:00:00Z",active:false},
];

it("writes each window in the reader's zone, says where it stands, and lists the ended ones last", () => inAmsterdam(async () => {
 vi.spyOn(globalThis,"fetch").mockResolvedValue(Response.json({maintenance:LISTED}));
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} canWrite={false}/>);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 await screen.findByText("Next deploy");
 const list=document.querySelector(".maintenance-list")!;
 const rows=[...list.querySelectorAll("li")].map((li)=>[li.querySelector("strong")!.textContent,li.getAttribute("data-state")]);
 expect(rows).toEqual([["Patch night","upcoming"],["Next deploy","upcoming"],["Last week's deploy","ended"],["Old migration","ended"]]);
 const text=list.textContent!;
 expect(text).toContain("1 Oct 2026, 20:00 → 21:00");
 expect(text).toContain("1 Sep 2026, 20:00 → 2 Sep 2026, 21:00");
 expect(text).toContain("Sunday at 02:00 (UTC), for 1 h");
 expect(text).toContain("— Ended");
 expect(text).toContain("— Upcoming");
 expect(text).not.toContain("Not active now");
 // No ISO timestamp from the API reaches the screen.
 expect(text).not.toMatch(/\d{4}-\d\d-\d\dT\d\d:\d\d/);
 // A viewer is told, and is offered nothing to remove.
 expect(screen.queryByRole("button",{name:"Remove ended windows"})).toBeNull();
}));

it("removes the ended one-off windows after a question that counts them, and keeps the rest", async () => {
 let listed=[...LISTED];
 const deleted:string[]=[];
 vi.spyOn(globalThis,"fetch").mockImplementation(async(url,init)=>{
  if(init?.method==="DELETE"){const id=String(url).split("/").pop()!;deleted.push(id);listed=listed.filter((w)=>String(w.id)!==id);return new Response(null,{status:204});}
  return Response.json({maintenance:listed});
 });
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 await screen.findByText("Next deploy");
 expect(screen.getByRole("button",{name:"Remove maintenance Old migration"}).textContent).toBe("Remove");
 expect(screen.getByRole("button",{name:"Cancel maintenance Next deploy"}).textContent).toBe("Cancel maintenance");
 fireEvent.click(screen.getByRole("button",{name:"Remove ended windows"}));
 const question=screen.getByRole("group",{name:/Remove 2 ended windows\?/});
 expect(question.textContent).toContain("Checks they excluded from uptime stay excluded.");
 await waitFor(()=>expect(document.activeElement).toBe(screen.getByRole("button",{name:"Keep them"})));
 // Keeping them sends nothing and puts focus back on the button that asked.
 fireEvent.click(screen.getByRole("button",{name:"Keep them"}));
 await waitFor(()=>expect(document.activeElement).toBe(screen.getByRole("button",{name:"Remove ended windows"})));
 expect(deleted).toEqual([]);
 fireEvent.click(screen.getByRole("button",{name:"Remove ended windows"}));
 fireEvent.click(screen.getByRole("button",{name:"Remove 2 ended windows"}));
 await screen.findByText("Removed 2 ended windows. Recorded history is unchanged.");
 expect(deleted).toEqual(["1","3"]);
 await waitFor(()=>expect(document.querySelectorAll(".maintenance-list li")).toHaveLength(2));
 expect(screen.queryByRole("button",{name:"Remove ended windows"})).toBeNull();
});

it("says how far removing ended windows got when a request fails", async () => {
 let listed=[...LISTED];
 vi.spyOn(globalThis,"fetch").mockImplementation(async(url,init)=>{
  if(init?.method==="DELETE"){
   const id=String(url).split("/").pop()!;
   if(id==="3") return Response.json({error:"database unavailable"},{status:500});
   listed=listed.filter((w)=>String(w.id)!==id);return new Response(null,{status:204});
  }
  return Response.json({maintenance:listed});
 });
 render(<LiveMonitorsRoot client={new QueryClient({defaultOptions:{queries:{retry:false}}})} fetchMonitors={async()=>[]} />);
 fireEvent.click(await screen.findByRole("button",{name:"Maintenance"}));
 await screen.findByText("Next deploy");
 fireEvent.click(screen.getByRole("button",{name:"Remove ended windows"}));
 fireEvent.click(screen.getByRole("button",{name:"Remove 2 ended windows"}));
 expect((await screen.findByRole("alert")).textContent).toBe("Removed 1 of 2 ended windows, then stopped: database unavailable");
 await waitFor(()=>expect(document.activeElement).toBe(screen.getByRole("button",{name:"Remove ended windows"})));
 expect(screen.getByText("Old migration")).toBeTruthy();
});
