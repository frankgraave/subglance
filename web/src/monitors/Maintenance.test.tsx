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
