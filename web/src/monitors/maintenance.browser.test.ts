// @vitest-environment node
import { afterAll, beforeAll, expect, it } from "vitest";
import { execFile, spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { createInterface, type Interface } from "node:readline";
import axe from "axe-core";
import { chromium, type Browser } from "../layout/harness/browser";
import { THEME_STORAGE_KEY } from "../theme/theme";
const root = fileURLToPath(new URL("../../../", import.meta.url));
let dir: string, child: ChildProcess, lines: Interface, browser: Browser;
let fixture: { url: string; session: string; ids: number[] };
const readLine = () => new Promise<string>((resolve) => lines.once("line", resolve));
beforeAll(async () => {
 dir=await mkdtemp(join(tmpdir(),"maintenance-browser-"));
 const binary=join(dir,"fixture");
 await promisify(execFile)("go",["build","-p","1","-o",binary,"./internal/api/testdata/maintenance-browser"],{cwd:root});
 child=spawn(binary,[join(dir,"history.db")],{stdio:["pipe","pipe","pipe"]});
 lines=createInterface({input:child.stdout!});
 fixture=JSON.parse(await Promise.race([readLine(),new Promise<string>((_,reject)=>{child.once("error",reject);child.once("exit",code=>reject(new Error(`fixture exited ${code}`)));})]));
 browser=await chromium();
},120_000);
afterAll(async()=>{await browser?.close();lines?.close();if(child&&child.exitCode===null){const exited=new Promise<void>(resolve=>child.once("exit",()=>resolve()));child.stdin?.end();await exited;}if(dir)await rm(dir,{recursive:true,force:true});});
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
  expect(await check(true)).toMatchObject({added_alerts:0});
  await page.goto(`${fixture.url}/monitors/${id}`,{waitUntil:"domcontentloaded"});
  await page.waitForFunction(()=>document.querySelector('.response-history')?.textContent?.includes("Maintenance — alerts suppressed; excluded from uptime"));
  expect(await page.$eval('.mon-detail-windows',el=>el.textContent)).toContain("3 maintenance checks excluded");
  expect(errors).toEqual([]);
 }catch(error){
  console.log(await page.evaluate(()=>({text:document.querySelector('.maintenance')?.textContent,buttons:Array.from(document.querySelectorAll('.maintenance button')).map(el=>{const r=el.getBoundingClientRect();return {name:el.getAttribute('aria-label'),disabled:(el as HTMLButtonElement).disabled,rect:{top:r.top,left:r.left,width:r.width,height:r.height},hit:document.elementFromPoint(r.left+r.width/2,r.top+r.height/2)?.outerHTML}})})));
  throw error;
 }finally{await context.close();}
});
