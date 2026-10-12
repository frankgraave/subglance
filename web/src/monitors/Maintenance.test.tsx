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
