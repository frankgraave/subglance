// @vitest-environment jsdom
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RoleChoice } from "./RoleChoice";

afterEach(cleanup);

const webSrc = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("RoleChoice", () => {
  it("offers the three roles, least privilege first, and marks the chosen one", () => {
    render(<RoleChoice label="Role" value="editor" onChange={() => {}} />);
    const buttons = within(screen.getByRole("group", { name: "Role" })).getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual(["Viewer", "Editor", "Admin"]);
    expect(buttons.map((b) => b.getAttribute("aria-pressed"))).toEqual(["false", "true", "false"]);
  });

  it("offers only the roles it is given", () => {
    render(<RoleChoice label="Role" value="viewer" roles={["viewer", "editor"]} onChange={() => {}} />);
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Viewer", "Editor"]);
  });

  it("reports the role pressed and leaves saving to the caller", () => {
    const onChange = vi.fn();
    render(<RoleChoice label="Role for a@example.com" value="viewer" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Admin" }));
    expect(onChange).toHaveBeenCalledWith("admin");
  });

  it("refuses presses while disabled, and keeps showing the chosen role", () => {
    const onChange = vi.fn();
    render(<RoleChoice label="Role" value="editor" disabled onChange={onChange} />);
    const buttons = screen.getAllByRole("button") as HTMLButtonElement[];
    expect(buttons.every((b) => b.disabled)).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Admin" }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Editor" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("ties help text to the group", () => {
    render(<RoleChoice label="Role" value="viewer" describedBy="help" onChange={() => {}} />);
    expect(screen.getByRole("group", { name: "Role" }).getAttribute("aria-describedby")).toBe("help");
  });
});

/** Every non-test .tsx under web/src, as [path relative to web/src, source]. */
function sources(dir = webSrc): [string, string][] {
  return readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) return sources(path);
    if (!entry.endsWith(".tsx") || entry.includes(".test.")) return [];
    return [[relative(webSrc, path), readFileSync(path, "utf8")] as [string, string]];
  });
}

/**
 * The shapes a hand-built role picker takes: a select named for a role, a
 * segmented control labelled as one, or the role list mapped into options.
 */
const HAND_BUILT = [
  /<select\b[^>]*\b(?:id|aria-label|name)=\{?[^>]*role/gi,
  /<SegmentedControl\b[^>]*\blabel=\{?["'`][^"'`]*\brole\b/gi,
  /\broles?\s*\.map\(/gi,
];

function handBuilt(source: string): string[] {
  return HAND_BUILT.flatMap((pattern) => [...source.matchAll(pattern)].map((match) => match[0]));
}

/*
 * Users drew its roles as a native select and API tokens as a segmented
 * control, for the same three roles. Each was reasonable on its own; together
 * they were two controls to learn for one choice. A role is chosen with
 * RoleChoice, and this fails on a picker built anywhere else.
 */
describe("a role is chosen with RoleChoice, never a hand-built picker", () => {
  it("builds a role picker only in RoleChoice.tsx", () => {
    const found = sources()
      .filter(([path]) => path !== join("components", "RoleChoice.tsx"))
      .flatMap(([path, source]) => handBuilt(source).map((match) => `${path}: ${match}`));
    expect(found).toEqual([]);
  });

  it("recognises each shape, so the scan above cannot pass by matching nothing", () => {
    expect(handBuilt('<select className="input" id={`${id}-role`} value={role}>')).toHaveLength(1);
    expect(handBuilt('<select aria-label={`Role for ${email}`} value={role}>')).toHaveLength(1);
    expect(handBuilt('<SegmentedControl label="Role" value={scope} />')).toHaveLength(1);
    expect(handBuilt("{ROLES.map((item) => <option key={item}>{item}</option>)}")).toHaveLength(1);
    expect(handBuilt("roles.map((item) => ({ id: item, label: item }))")).toHaveLength(1);
    // Not a role picker: a select about something else, a segmented control
    // with another subject, and the word in prose.
    expect(handBuilt('<select id={`${id}-expiry`} value={expiry}>')).toEqual([]);
    expect(handBuilt('<SegmentedControl label="Dashboard layout" value={layout} />')).toEqual([]);
    expect(handBuilt("A token acts with its own role, never more than yours.")).toEqual([]);
  });
});
