// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  TITLE_MORPH,
  detailTitle,
  monitorTitleLink,
  morphNavigation,
} from "./viewTransition";

/**
 * SUB-23: opening a monitor morphs instead of swapping the page.
 *
 * jsdom has neither the View Transitions API nor layout, so both are stubbed
 * here and these tests pin the contract around them: the update always runs,
 * motion is skipped when it is not wanted, and the shared name is held by at
 * most one element per screen and never outlives the transition. Whether the
 * browser actually animates is `viewTransition.browser.test.ts`.
 */

type Doc = { startViewTransition?: unknown };
const doc = document as unknown as Doc;

function box(element: Element, top = 10) {
  element.getBoundingClientRect = () =>
    ({ top, left: 10, bottom: top + 20, right: 200, width: 190, height: 20 }) as DOMRect;
}

function motion(reduce: boolean) {
  window.matchMedia = ((query: string) => ({
    matches: reduce && query.includes("reduce"),
    media: query,
  })) as unknown as typeof window.matchMedia;
}

/** A stand-in for the browser: runs the update now, finishes on demand. */
function fakeTransitions() {
  let finish: () => void = () => {};
  const namesDuringUpdate: string[][] = [];
  const start = vi.fn((update: () => void) => {
    update();
    namesDuringUpdate.push(
      [...document.querySelectorAll<HTMLElement>("*")]
        .filter((el) => el.style.getPropertyValue("view-transition-name") === TITLE_MORPH)
        .map((el) => el.id),
    );
    return { finished: new Promise<void>((resolve) => (finish = resolve)) };
  });
  doc.startViewTransition = start;
  return { start, namesDuringUpdate, finish: () => finish() };
}

afterEach(() => {
  delete doc.startViewTransition;
  // @ts-expect-error jsdom has no matchMedia; tests add and remove it.
  delete window.matchMedia;
  document.body.innerHTML = "";
});

function screen() {
  document.body.innerHTML = `
    <main>
      <a id="row" href="/monitors/7">api</a>
      <h1 id="title" class="mon-detail-name">api</h1>
    </main>`;
  const row = document.getElementById("row")!;
  const title = document.getElementById("title")!;
  box(row);
  box(title);
  return { row, title };
}

describe("morphNavigation", () => {
  it("still navigates in a browser without view transitions", () => {
    motion(false);
    const update = vi.fn();
    morphNavigation(update);
    expect(update).toHaveBeenCalledOnce();
  });

  it("does not animate when the reader asked for reduced motion", () => {
    motion(true);
    const browser = fakeTransitions();
    const update = vi.fn();
    morphNavigation(update);
    expect(update).toHaveBeenCalledOnce();
    expect(browser.start).not.toHaveBeenCalled();
  });

  it("does not animate when motion cannot be asked about", () => {
    const browser = fakeTransitions();
    const update = vi.fn();
    morphNavigation(update);
    expect(update).toHaveBeenCalledOnce();
    expect(browser.start).not.toHaveBeenCalled();
  });

  it("hands the title from the clicked row to the page heading", async () => {
    motion(false);
    const { row, title } = screen();
    const browser = fakeTransitions();
    const names = () => [row, title].map((el) => el.style.getPropertyValue("view-transition-name"));

    morphNavigation(() => {}, { from: () => row, to: () => title });
    // Old screen: only the row. New screen: only the heading. Two holders of
    // one name on a screen make the browser skip the transition entirely.
    expect(browser.namesDuringUpdate).toEqual([["title"]]);
    expect(names()).toEqual(["", TITLE_MORPH]);

    browser.finish();
    await Promise.resolve();
    await Promise.resolve();
    expect(names()).toEqual(["", ""]);
  });

  it("tags the old element before the browser captures the old screen", () => {
    motion(false);
    const { row } = screen();
    let atCapture = "";
    doc.startViewTransition = (update: () => void) => {
      atCapture = row.style.getPropertyValue("view-transition-name");
      update();
      return { finished: Promise.resolve() };
    };
    morphNavigation(() => {}, { from: () => row });
    expect(atCapture).toBe(TITLE_MORPH);
  });

  it("lets a title outside the viewport cross-fade instead of fly in", () => {
    motion(false);
    const { row, title } = screen();
    box(row, 5000);
    const browser = fakeTransitions();
    morphNavigation(() => {}, { from: () => row, to: () => title });
    expect(row.style.getPropertyValue("view-transition-name")).toBe("");
    expect(browser.start).toHaveBeenCalledOnce();
  });

  it("clears the name when the transition is skipped", async () => {
    motion(false);
    const { title } = screen();
    doc.startViewTransition = (update: () => void) => {
      update();
      return { finished: Promise.reject(new Error("skipped")) };
    };
    morphNavigation(() => {}, { to: () => title });
    await Promise.resolve();
    await Promise.resolve();
    expect(title.style.getPropertyValue("view-transition-name")).toBe("");
  });
});

describe("finding the title", () => {
  it("picks the visible link to the monitor, not a hidden copy or another monitor", () => {
    document.body.innerHTML = `
      <main>
        <a id="other" href="/monitors/8">db</a>
        <a id="hidden" href="/monitors/7">api</a>
        <a id="shown" href="/monitors/7">api</a>
      </main>`;
    const main = document.querySelector("main")!;
    for (const el of main.querySelectorAll("a")) box(el);
    document.getElementById("hidden")!.getBoundingClientRect = () =>
      ({ top: 0, left: 0, bottom: 0, right: 0, width: 0, height: 0 }) as DOMRect;
    expect(monitorTitleLink(main, "7")?.id).toBe("shown");
    expect(monitorTitleLink(main, "9")).toBeNull();
    expect(monitorTitleLink(null, "7")).toBeNull();
  });

  it("finds the detail page heading", () => {
    const { title } = screen();
    expect(detailTitle(document.body)).toBe(title);
    expect(detailTitle(null)).toBeNull();
  });
});
