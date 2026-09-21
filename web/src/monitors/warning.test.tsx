// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, it, expect } from "vitest";
import { applyHeartbeat, statusAfterHeartbeat, statusAfterEvent } from "../live/apply";
import { parseEvent } from "../live/events";
import { incidentStory } from "../incidents/story";
import { IncidentStoryItem } from "../incidents/IncidentStoryItem";
import { fromApi } from "./types";
import { Led } from "./Led";
import { ResponseHistory } from "./ResponseHistory";
import { HeartbeatBar } from "../heartbeat/HeartbeatBar";

afterEach(cleanup);

describe("unconfirmed warnings", () => {
 it("distinguishes warning from pending and confirmed down in the live path", () => {
  expect(statusAfterHeartbeat("up", false)).toBe("warning");
  expect(statusAfterEvent("incident_opened")).toBe("warning");
  expect(statusAfterHeartbeat("down", false)).toBe("down");
 });
 it("names the warning in the lamp and withdraws the present tense when stale", () => {
  // The wire status must be usable before the union grows.
  const status = "warning" as Parameters<typeof Led>[0]["status"];
  const {rerender} = render(<Led status={status} />);
  expect(screen.getByText("Warning")).toBeTruthy();
  rerender(<Led status={status} stale />);
  expect(screen.getByText("Was warning")).toBeTruthy();
 });
 it("keeps evidence and explains why the failure did not alert", () => {
  render(<ResponseHistory heartbeats={[{id:"1", ts:new Date().toISOString(), ok:false, assessment:"warning", error:"unexpected status 503", response:{body:"retry later"}}]} />);
  expect(screen.getByText(/Warning.*unconfirmed/)).toBeTruthy();
  expect(screen.getByText("unexpected status 503")).toBeTruthy();
  expect(screen.getByText("retry later")).toBeTruthy();
 });
 it("does not label a warning bar Down or count it as downtime", () => {
  const {container} = render(<HeartbeatBar label="Warning check" width={300} framed beats={[{ts:1,ok:false,assessment:"warning",latencyMs:5}]} />);
  expect(container.querySelector('.hb-bar--warning')).not.toBeNull();
  expect(container.textContent).toContain("No eligible checks");
 });
});

it("a compressed bucket keeps confirmed downtime ahead of a later warning", () => {
 const {container} = render(<HeartbeatBar label="Mixed checks" width={5} beats={[
  {ts:1,ok:false,assessment:"down",latencyMs:5,error:"confirmed"},
  {ts:2,ok:false,assessment:"warning",latencyMs:5,error:"unconfirmed"},
 ]} />);
 expect(container.querySelector('.hb-bar--down')).not.toBeNull();
 expect(container.querySelector('.hb-bar--warning')).toBeNull();
});

it("carries the recorded assessment from SSE through the live cache", () => {
 const event = parseEvent("heartbeat", JSON.stringify({ monitor_id: 1, at: new Date().toISOString(), data: { ok:false,assessment:"warning",error:"failed" } }));
 expect(event?.kind).toBe("heartbeat");
 if (event?.kind !== "heartbeat") throw new Error("heartbeat missing");
 expect(event.assessment).toBe("warning");
 const [result] = applyHeartbeat([{id:"1",name:"api",status:"up",target:"https://example.invalid",latencyMs:null,uptime24h:null,beats:[],lastCheck:null,tags:{}}],event);
 expect(result.status).toBe("warning");
 expect(result.beats[0].assessment).toBe("warning");
});

it.each([false, true])("never calls an unconfirmed incident downtime, resolved=%s", (resolved) => {
 const incident = {id:"1",monitorId:"1",startedAt:1000,confirmedAt:null,resolvedAt:resolved?2000:null,ackedAt:null,confirmed:false,resolved,acked:false,durationS:1};
 const story = incidentStory(incident,3000);
 expect(story.began).toMatch(/^Warning/);
 expect(story.sentence).toContain("No alert");
 expect(story.sentence).not.toMatch(/down|escalating/i);
 render(<IncidentStoryItem incident={incident} now={3000} onAck={() => {}} />);
 expect(document.querySelector('.led[data-status="down"]')).toBeNull();
 expect(screen.queryByRole("button",{name:/Mute repeat alerts/})).toBeNull();
});


it("preserves warning assessment from a list API response", () => {
 const monitor = fromApi({id:1,name:"api",type:"http",target:"https://example.invalid",interval_s:300,timeout_s:10,enabled:true,status:"warning",uptime_24h:null,created_at:new Date().toISOString(),heartbeats:[{ts:new Date().toISOString(),ok:false,assessment:"warning"}]});
 expect(monitor.status).toBe("warning");
 expect(monitor.beats[0].assessment).toBe("warning");
 expect(monitor.uptime24h).toBeNull();
});
