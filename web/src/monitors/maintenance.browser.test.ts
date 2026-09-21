// @vitest-environment node
import { afterAll, afterEach, beforeAll, beforeEach, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface, type Interface } from "node:readline";
import axe from "axe-core";
import { chromium, type Browser } from "../layout/harness/browser";
import { maintenanceCleanup } from "../layout/harness/maintenanceCleanup";
import { THEME_STORAGE_KEY } from "../theme/theme";
const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string, child: ChildProcess, lines: Interface, browser: Browser;
let fixture: { url: string; session: string; ids: number[] };
let cleanup: ReturnType<typeof maintenanceCleanup> | undefined;
const readLine = () => new Promise<string>((resolve) => lines.once("line", resolve));
beforeAll(async () => {
 dir=await mkdtemp(join(tmpdir(),"maintenance-browser-"));
 const binary=join(dir,"fixture");
 await promisify(execFile)("go",["build","-p","1","-o",binary,"./internal/api/testdata/maintenance-browser"],{cwd:root});
 child=spawn(binary,[join(dir,"history.db")],{stdio:["pipe","pipe","pipe"]});
 lines=createInterface({input:child.stdout!});
 fixture=JSON.parse(await Promise.race([readLine(),new Promise<string>((_,reject)=>{child.once("error",reject);child.once("exit",code=>reject(new Error(`fixture exited ${code}`)));})]));
 cleanup=maintenanceCleanup(fixture.url,fixture.session);
 browser=await chromium();
},120_000);
beforeEach(async()=>{
 // Retry before another case can observe a schedule left by a failed cleanup.
 expect(await cleanup?.()).toEqual([]);
});
afterEach(async()=>{await cleanup?.();});
afterAll(async()=>{
 let failures: string[] = [];
 try { failures=await cleanup?.() ?? []; }
 finally {
  await browser?.close();lines?.close();
  if(child&&child.exitCode===null){const exited=new Promise<void>(resolve=>child.once("exit",()=>resolve()));child.stdin?.end();await exited;}
  if(dir)await rm(dir,{recursive:true,force:true});
 }
 expect(failures).toEqual([]);
});
it.each([["dark",375,0],["light",375,1],["dark",1440,2],["light",1440,3]] as const)("maintenance through real UI/API/checker: %s %ipx",async(theme,width,index)=>{
 const context=await browser.createBrowserContext();const page=await context.newPage();const id=fixture.ids[index];
 page.setDefaultTimeout(5_000);
 const errors:string[]=[];page.on("pageerror",error=>errors.push(String(error)));
 const check=async(healthy:boolean)=>{const response=readLine();child.stdin!.write(`${JSON.stringify({index,healthy})}\n`);return JSON.parse(await response);};
 try{
  await context.setCookie({name:"subglance_session",value:fixture.session,domain:new URL(fixture.url).hostname,path:"/",httpOnly:true,sameSite:"Strict"});
  await page.setViewport({width,height:1000});
  await page.evaluateOnNewDocument((key,value)=>localStorage.setItem(key,value),THEME_STORAGE_KEY,theme);
  await page.goto(`${fixture.url}/monitors`,{waitUntil:"domcontentloaded"});
  await (await page.waitForSelector('.maintenance summary'))!.click();
  await page.waitForFunction(()=>document.querySelector('.maintenance')?.textContent?.includes("No maintenance windows scheduled."));
  await page.type('.maintenance input[name="name"]',`Deploy ${index}`);
  await page.select('.maintenance select[name="monitor_id"]',String(id));
  const now=Date.now();
  await page.$eval('.maintenance input[name="starts_at"]',(el,v)=>(el as HTMLInputElement).value=v,new Date(now-60_000).toISOString().slice(0,16));
  await page.$eval('.maintenance input[name="ends_at"]',(el,v)=>(el as HTMLInputElement).value=v,new Date(now+3_600_000).toISOString().slice(0,16));
  await page.click('.maintenance button[type="submit"]');
  await page.waitForFunction(()=>document.querySelector('.maintenance')?.textContent?.includes("Maintenance scheduled."));
  expect(await check(false)).toMatchObject({added_alerts:0});
  expect(await check(false)).toMatchObject({added_alerts:0});
  const state=await page.evaluate(async(id)=>({monitor:await fetch(`/api/v1/monitors/${id}`).then(r=>r.json()),uptime:await fetch(`/api/v1/monitors/${id}/uptime`).then(r=>r.json()),beats:await fetch(`/api/v1/monitors/${id}/heartbeats`).then(r=>r.json())}),id);
  expect(state.monitor).toMatchObject({status:"down",maintenance:true,uptime_24h:null});
  expect(state.uptime.windows[0]).toMatchObject({total:0,maintenance:2,uptime:null});
  expect(state.beats.heartbeats.map((b:{assessment:string;maintenance:boolean})=>[b.assessment,b.maintenance])).toEqual([["down",true],["warning",true]]);
  // The form can also schedule an exact tag group every week.
  await page.select('.maintenance select[name="scope"]',"tag");
  await page.type('.maintenance input[name="tag_key"]',"env");await page.type('.maintenance input[name="tag_value"]',"prod");
  await page.select('.maintenance select[name="schedule"]',"weekly");
  const weeklyStart=new Date();
  const day=weeklyStart.getUTCDay();
  const clock=weeklyStart.toISOString().slice(11,16);
  await page.$$eval('.maintenance input[name="weekdays"]',(els,day)=>els.forEach(el=>(el as HTMLInputElement).checked=Number((el as HTMLInputElement).value)===day),day);
  await page.$eval('.maintenance input[name="local_time"]',(el,clock)=>(el as HTMLInputElement).value=clock,clock);
  await page.$eval('.maintenance input[name="name"]',(el)=>(el as HTMLInputElement).value="Weekly deploy");
  await page.click('.maintenance button[type="submit"]');
  await page.waitForFunction(()=>document.querySelectorAll('.maintenance-list li').length===2);
  expect(await page.$eval('.maintenance-list',(el)=>el.textContent)).toContain(`at ${clock} (UTC), 60 minutes`);
  await page.evaluate(axe.source);
  const audit=await page.evaluate(async()=> (window as unknown as {axe:typeof axe}).axe.run({include:[".maintenance"]},{runOnly:{type:"tag",values:["wcag2a","wcag2aa"]}}));
  expect(audit.violations).toEqual([]);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  if(process.env.MAINTENANCE_BROWSER_PROOF_DIR){await mkdir(process.env.MAINTENANCE_BROWSER_PROOF_DIR,{recursive:true});await page.screenshot({path:join(process.env.MAINTENANCE_BROWSER_PROOF_DIR,`maintenance-${theme}-${width}.png`),fullPage:true});}
  // A real stream interruption must withdraw the present-tense claim too.
  await page.goto(`${fixture.url}/monitors/${id}`,{waitUntil:"domcontentloaded"});
  await page.waitForFunction(()=>Array.from(document.querySelectorAll('[role="status"]')).some(el=>el.textContent==="Scheduled maintenance — checks continue; alerts suppressed."));
  await page.waitForFunction(()=>document.querySelector(".inc-reminders")?.textContent?.includes("Reminders suspended — a maintenance window is active."));
  expect(await page.$(".inc-reminders time")).toBeNull();
  await page.setOfflineMode(true);
  // Network emulation does not close an established loopback SSE socket.
  const disconnected=readLine();child.stdin!.write(`${JSON.stringify({disconnect:true})}\n`);await disconnected;
  await page.waitForFunction(()=>Array.from(document.querySelectorAll('[role="status"]')).some(el=>el.textContent==="Scheduled maintenance when we last heard — checks continued; alerts were suppressed."));
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await page.evaluate(axe.source);
  const staleAudit=await page.evaluate(async()=> (window as unknown as {axe:typeof axe}).axe.run({include:['[role="status"]']},{runOnly:{type:"tag",values:["wcag2a","wcag2aa"]}}));
  expect(staleAudit.violations).toEqual([]);
  if(process.env.MAINTENANCE_BROWSER_PROOF_DIR) await page.screenshot({path:join(process.env.MAINTENANCE_BROWSER_PROOF_DIR,`maintenance-stale-${theme}-${width}.png`),fullPage:true});
  await page.setOfflineMode(false);
  await page.waitForFunction(()=>Array.from(document.querySelectorAll('[role="status"]')).some(el=>el.textContent==="Scheduled maintenance — checks continue; alerts suppressed."));
  await page.goto(`${fixture.url}/monitors`,{waitUntil:"domcontentloaded"});
  await (await page.waitForSelector('.maintenance summary'))!.click();
  const cancel=await page.waitForSelector(`[aria-label="Cancel maintenance Deploy ${index}"]:not([disabled])`);
  await cancel!.evaluate(el=>el.scrollIntoView({block:"center"}));
  await cancel!.click();
  await page.waitForFunction(()=>document.querySelectorAll('.maintenance-list li').length===1);
  // Removing the one-off window leaves the recurring tag group active.
  expect(await check(false)).toMatchObject({added_alerts:0});
  const groupState=await page.evaluate(async(id)=>fetch(`/api/v1/monitors/${id}`).then(r=>r.json()),id);
  expect(groupState.maintenance).toBe(true);
  const weeklyCancel=await page.waitForSelector('[aria-label="Cancel maintenance Weekly deploy"]:not([disabled])');
  await weeklyCancel!.evaluate(el=>el.scrollIntoView({block:"center"}));
  await weeklyCancel!.click();
  await page.waitForFunction(()=>document.querySelector('.maintenance')?.textContent?.includes("No maintenance windows scheduled."));
  await page.goto(`${fixture.url}/monitors/${id}`,{waitUntil:"domcontentloaded"});
  await page.waitForFunction(()=>document.querySelector(".inc-reminders")?.textContent?.includes("Reminders suspended — the initial alert is pending after maintenance."));
  expect(await page.$(".inc-reminders time")).toBeNull();
  expect(await check(true)).toMatchObject({added_alerts:0});
  await page.goto(`${fixture.url}/monitors/${id}`,{waitUntil:"domcontentloaded"});
  await page.waitForFunction(()=>document.querySelector('.response-history')?.textContent?.includes("Maintenance — alerts suppressed; excluded from uptime"));
  expect(await page.$eval('.mon-detail-windows',el=>el.textContent)).toContain("3 maintenance checks excluded");
  expect(errors).toEqual([]);
 }catch(error){
  try {
   console.log(await page.evaluate(()=>({text:document.querySelector('.maintenance')?.textContent,buttons:Array.from(document.querySelectorAll('.maintenance button')).map(el=>{const r=el.getBoundingClientRect();return {name:el.getAttribute('aria-label'),disabled:(el as HTMLButtonElement).disabled,rect:{top:r.top,left:r.left,width:r.width,height:r.height},hit:document.elementFromPoint(r.left+r.width/2,r.top+r.height/2)?.outerHTML}})})));
  }catch(diagnosticError){
   console.error("Maintenance diagnostic collection failed:", diagnosticError);
  }
  throw error;
 }finally{await context.close();}
});

it.each([["dark",375,4],["light",375,5],["dark",1440,6],["light",1440,7]] as const)("natural maintenance boundaries update the open detail: %s %ipx", async(theme,width,index)=>{
 const context=await browser.createBrowserContext();const page=await context.newPage();const id=fixture.ids[index];
 page.setDefaultTimeout(5_000);
 const check=async(healthy:boolean)=>{const response=readLine();child.stdin!.write(`${JSON.stringify({index,healthy})}\n`);return JSON.parse(await response);};
 try {
  await context.setCookie({name:"subglance_session",value:fixture.session,domain:new URL(fixture.url).hostname,path:"/",httpOnly:true,sameSite:"Strict"});
  await page.setViewport({width,height:1000});
  await page.evaluateOnNewDocument((key,value)=>localStorage.setItem(key,value),THEME_STORAGE_KEY,theme);
  await page.goto(`${fixture.url}/monitors/${id}`,{waitUntil:"domcontentloaded"});
  await page.waitForFunction(()=>document.querySelector('.mon-detail-uptime-note'));
  const end=await page.evaluate(async(id)=>{
    const start=Date.now()+1000;const end=start+10_000;
    const res=await fetch('/api/v1/maintenance',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:'Boundary',monitor_id:id,starts_at:new Date(start).toISOString(),ends_at:new Date(end).toISOString()})});
    if(!res.ok) throw new Error(`schedule ${res.status}`);
    return {start,end};
  },id);
  expect(await page.$$eval('[role="status"]',els=>els.some(el=>el.textContent?.includes('Scheduled maintenance')))).toBe(false);
  await page.waitForFunction(start=>Date.now()>=start,{},end.start);
  expect(await check(false)).toMatchObject({added_alerts:0});
  expect(await check(false)).toMatchObject({added_alerts:0});
  await page.waitForFunction(()=>Array.from(document.querySelectorAll('[role="status"]')).some(el=>el.textContent?.includes('Scheduled maintenance')));
  expect(await page.$eval('.chart-breakdown',el=>el.textContent)).toBe('No eligible checks');
  expect(await page.$eval('.chart-headline',el=>el.textContent)).toBe('—');
  await page.waitForFunction(end=>Date.now()>=end,{timeout:15_000},end.end);
  expect(await check(true)).toMatchObject({added_alerts:0});
  await page.waitForFunction(()=>!Array.from(document.querySelectorAll('[role="status"]')).some(el=>el.textContent?.includes('Scheduled maintenance')));
  expect(await page.$eval('.chart-headline',el=>el.textContent)).toBe('100.00%');
  expect(await page.$eval('.chart-breakdown',el=>el.textContent)).toBe('1 eligible check · 0 confirmed down');
  const history=await page.evaluate(async(id)=>fetch(`/api/v1/monitors/${id}/heartbeats`).then(r=>r.json()),id);
  expect(history.heartbeats.map((b:{maintenance:boolean})=>b.maintenance)).toEqual([false,true,true]);
  await page.evaluate(axe.source);
  const audit=await page.evaluate(async()=> (window as unknown as {axe:typeof axe}).axe.run({include:['.chart','[role="status"]']},{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));
  expect(audit.violations).toEqual([]);
  // After the empty track gains chart chrome, resizing must measure its new
  // DOM node. A retained width can accidentally fit until the viewport changes.
  for (const resizedWidth of [width + 90, width]) {
   await page.setViewport({width:resizedWidth,height:1000});
   await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
   const difference=await page.$eval('.hb-track',el=>el.getBoundingClientRect().width-el.querySelector('svg')!.getBoundingClientRect().width);
   expect(difference).toBeGreaterThanOrEqual(0);
   expect(difference).toBeLessThan(9);
  }
  await page.focus('.hb-track');
  await page.keyboard.press('End');
  await page.keyboard.press('ArrowLeft');
  await page.waitForFunction(()=>document.querySelector('.hb-tooltip')?.textContent?.includes('excluded from uptime'));
  await page.keyboard.press('Escape');
  await page.waitForFunction(()=>!document.querySelector('.hb-tooltip'));
  expect(new URL(page.url()).pathname).toBe(`/monitors/${id}`);
  // Screenreader table cells retain their intrinsic rectangles inside the
  // clipped caption. Exclude them only while that clipping actually holds.
  const overflow=await page.evaluate(()=>Array.from(document.querySelectorAll('main *')).filter(el=>{
   const caption=el.closest('.hb-sr-only');
   if(caption){
    const style=getComputedStyle(caption);const rect=caption.getBoundingClientRect();
    if(style.clipPath==='inset(50%)' && style.overflow==='hidden' && rect.width<=1 && rect.height<=1) return false;
   }
   return el.getBoundingClientRect().right>innerWidth+1;
  }).map(el=>({tag:el.tagName,cls:el.className,text:el.textContent?.slice(0,160),width:el.getBoundingClientRect().width})).slice(0,15));
  expect(overflow).toEqual([]);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 } finally {await context.close();}
});

it.each([["dark",375],["light",375],["dark",1440],["light",1440]] as const)("maintenance action keeps contrast during hover: %s %ipx", async(theme,width)=>{
 const context=await browser.createBrowserContext();const page=await context.newPage();
 page.setDefaultTimeout(5_000);
 try {
  await context.setCookie({name:"subglance_session",value:fixture.session,domain:new URL(fixture.url).hostname,path:"/",httpOnly:true,sameSite:"Strict"});
  await page.setViewport({width,height:1000});
  await page.evaluateOnNewDocument((key,value)=>localStorage.setItem(key,value),THEME_STORAGE_KEY,theme);
  await page.goto(`${fixture.url}/monitors`,{waitUntil:"domcontentloaded"});
  await (await page.waitForSelector('.maintenance summary'))!.click();
  const button=await page.waitForSelector('.maintenance button[type="submit"]');
  await button!.evaluate(el=>el.scrollIntoView({block:"center"}));
  await page.evaluate(axe.source);
  for (const hovered of [true,false]) {
   if (hovered) await button!.hover(); else await page.mouse.move(0,0);
   // Freeze real CSS transitions between the resting and hovered colours.
   // A settled-state audit misses white ink on the still-light background.
   await button!.evaluate(el=>{
    for (const animation of el.getAnimations()) {
     animation.pause();
     animation.currentTime=Number(animation.effect!.getTiming().duration)/4;
    }
   });
   const audit=await page.evaluate(async()=> (window as unknown as {axe:typeof axe}).axe.run({include:['.maintenance button[type="submit"]']},{runOnly:{type:"tag",values:["wcag2a","wcag2aa"]}}));
   expect(audit.violations,`hover=${hovered}`).toEqual([]);
   await button!.evaluate(el=>el.getAnimations().forEach(animation=>animation.finish()));
  }
 } finally {await context.close();}
});
