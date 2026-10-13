import { afterAll, beforeAll, describe, expect, it } from "vitest";
import axe from "axe-core";
import type { ElementHandle } from "puppeteer-core";
import { chromium, type Browser, type Page } from "./harness/browser";
import { serveBuild, type Server } from "./harness/server";
import { THEME_STORAGE_KEY } from "../theme/theme";

/*
 * The certificate a failed Test it offers, in the add form, in a real
 * browser: the 95-character fingerprint has to break inside a phone's drawer
 * rather than push it sideways, the warning and the facts have to pass the
 * contrast audit in both themes, and pressing "Trust this certificate" has
 * to send the pin with the retest. jsdom has no layout, so none of the first
 * two can be seen there.
 */

let browser: Browser; let server: Server;
beforeAll(async () => { server = await serveBuild(); browser = await chromium(); });
afterAll(async () => { await browser?.close(); await server?.close(); });

const FP = "3F:9A:0B:52:1C:7E:44:D0:9B:21:6A:88:F3:05:CE:71:2D:B4:60:19:7A:E2:55:0C:8D:3B:F1:46:A9:EE:07:C2";

async function fill(page: Page, selector: string, value: string) {
  await page.focus(selector); await page.keyboard.down("Control"); await page.keyboard.press("A"); await page.keyboard.up("Control");
  await page.keyboard.press("Backspace"); await page.keyboard.type(value);
}
async function button(page: Page, text: string) {
  const handle = await page.waitForFunction((word) => [...document.querySelectorAll("button")].find((node) => node.textContent?.trim() === word), {}, text);
  await (handle.asElement() as ElementHandle<HTMLButtonElement>).click();
}

async function session(width: number, theme: string) {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  const previews: Record<string, unknown>[] = [];
  await page.setViewport({ width, height: 900 });
  await page.emulateMediaFeatures([{ name: "prefers-reduced-motion", value: "reduce" }]);
  await page.evaluateOnNewDocument((key, value) => localStorage.setItem(key, value), THEME_STORAGE_KEY, theme);
  await page.setRequestInterception(true);
  page.on("request", async (request) => {
    const url = new URL(request.url());
    if (url.origin !== server.url) { await request.abort(); return; }
    if (url.pathname === "/api/v1/monitors/preview") {
      const body = JSON.parse(request.postData()!); previews.push(body);
      const pinned = body.trusted_cert_sha256 === FP;
      await request.respond({ status: 200, contentType: "application/json", body: JSON.stringify(pinned
        ? { ok: true, checked_at: new Date().toISOString(), latency_ms: 12, status_code: 200, type: "http", target: body.target }
        : { ok: false, checked_at: new Date().toISOString(), latency_ms: 12, kind: "tls", type: "http", target: body.target,
          error: "tls: failed to verify certificate: x509: certificate signed by unknown authority",
          certificate: { subject: "CN=pve.home.arpa,O=Proxmox Virtual Environment", issuer: "CN=Proxmox Virtual Environment,OU=PVE Cluster Manager CA",
            not_before: "2026-01-01T00:00:00Z", not_after: "2028-01-01T00:00:00Z", sha256: FP } }) });
      return;
    }
    await request.continue();
  });
  await page.goto(`${server.url}/monitors/new`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector(".form-column");
  return { context, page, previews };
}

for (const theme of ["dark", "light"]) for (const width of [390, 1440]) describe(`${theme} ${width}`, () => {
  it("shows the offered certificate without widening the drawer, passes the audit, and trusts it", async () => {
    const { context, page, previews } = await session(width, theme);
    try {
      await fill(page, '.form-column input[placeholder^="example.com"]', "https://pve.home.arpa:8006");
      await button(page, "Test it");
      const group = '[role="group"][aria-label="Certificate the test was offered"]';
      await page.waitForSelector(group);

      // Nothing in the drawer scrolls sideways, and the fingerprint stays
      // inside the form's column.
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      const fits = await page.$eval(group, (node) => {
        const form = node.closest("form")!.getBoundingClientRect();
        return [...node.querySelectorAll("dd")].every((dd) => {
          const box = dd.getBoundingClientRect();
          return box.left >= form.left - 1 && box.right <= form.right + 1 && dd.scrollWidth <= dd.clientWidth + 1;
        });
      });
      expect(fits).toBe(true);

      await page.addScriptTag({ content: axe.source });
      const findings = await page.evaluate(async (selector) => {
        const result = await (window as typeof window & { axe: typeof axe }).axe.run(document.querySelector(selector)!, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa"] } });
        return result.violations.map(({ id, nodes }) => ({ id, nodes: nodes.map(({ html, failureSummary }) => ({ html, failureSummary })) }));
      }, group);
      expect(findings).toEqual([]);

      await button(page, "Trust this certificate");
      await page.waitForFunction(() => document.querySelector(".result--good") !== null);
      expect(previews.at(-1)).toMatchObject({ target: "https://pve.home.arpa:8006", trusted_cert_sha256: FP });
      expect(await page.$(group)).toBeNull();
      expect(await page.evaluate((fp) => document.querySelector("form")!.textContent!.includes(fp), FP)).toBe(true);
    } finally { await context.close(); }
  });
});
