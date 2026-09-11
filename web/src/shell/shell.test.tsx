// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppShell } from "./AppShell";
import { LayoutSwitcher } from "./LayoutSwitcher";
import { Sidebar } from "./Sidebar";
import { Topbar } from "./Topbar";
import { useShellPreferences } from "./useShellPreferences";
import { useShellShortcuts } from "./useShortcuts";
import { LAYOUT_STORAGE_KEY, SIDEBAR_STORAGE_KEY } from "./preferences";

afterEach(cleanup);

function fakeStorage(initial: Record<string, string> = {}) {
  const data = new Map(Object.entries(initial));
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    seen: data,
  };
}

describe("Sidebar", () => {
  it("names the one destination that exists as the current page", () => {
    render(<Sidebar collapsed={false} />);
    const current = screen.getByText("Dashboard").closest("[aria-current]");
    expect(current).toBeTruthy();
  });

  it("does not promise the four screens that do not exist", () => {
    render(<Sidebar collapsed={false} />);
    // They are shown — the shape of the product is information — but never as
    // something you can press, and never with a fabricated count beside them.
    for (const label of ["Incidents", "Monitors", "Notifications", "Settings"]) {
      const item = screen.getByText(label).closest(".shell-nav-item")!;
      expect(item.getAttribute("data-state")).toBe("planned");
      expect(item.querySelector("a, button")).toBeNull();
    }
    expect(screen.getAllByText("Soon")).toHaveLength(4);
  });

  it("keeps the labels readable to a screen reader when collapsed", () => {
    render(<Sidebar collapsed />);
    // Collapsed is a rail, not a disappearance: the text stays in the DOM so
    // the icons are not five unnamed squares.
    expect(screen.getByText("Dashboard")).toBeTruthy();
    expect(document.querySelector('.shell-sidebar[data-collapsed="true"]')).toBeTruthy();
  });
});

describe("LayoutSwitcher", () => {
  it("offers all four layouts and reports the pressed one", () => {
    render(<LayoutSwitcher layout="compact" onChange={() => {}} />);
    const buttons = screen.getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual(["Rows", "Cards", "Compact", "Status wall"]);
    expect(screen.getByRole("button", { name: "Compact" }).getAttribute("aria-pressed")).toBe(
      "true",
    );
  });

  it("shows the layout actually rendered, not the overridden preference", () => {
    // A toolbar claiming Rows while cards are on screen is worse than one
    // admitting the narrow viewport won.
    render(<LayoutSwitcher layout="rows" effective="cards" onChange={() => {}} />);
    expect(screen.getByRole("button", { name: "Cards" }).getAttribute("aria-pressed")).toBe("true");
    expect(screen.getByRole("button", { name: "Rows" }).getAttribute("aria-pressed")).toBe("false");
  });

  it("reports the chosen layout", () => {
    const onChange = vi.fn();
    render(<LayoutSwitcher layout="rows" onChange={onChange} />);
    fireEvent.click(screen.getByRole("button", { name: "Status wall" }));
    expect(onChange).toHaveBeenCalledWith("wall");
  });
});

describe("Topbar", () => {
  const topbar = (over: Partial<Parameters<typeof Topbar>[0]> = {}) => (
    <Topbar
      sidebarCollapsed={false}
      onToggleSidebar={() => {}}
      layout="rows"
      onLayoutChange={() => {}}
      themePreference="system"
      onThemeChange={() => {}}
      workbenchOpen={false}
      onToggleWorkbench={() => {}}
      {...over}
    />
  );

  it("puts the shortcut in the button's accessible name", () => {
    render(topbar());
    const button = screen.getByRole("button", { name: /collapse sidebar \(ctrl\+b\)/i });
    expect(button.getAttribute("aria-expanded")).toBe("true");
  });

  it("says expand once collapsed", () => {
    render(topbar({ sidebarCollapsed: true }));
    const button = screen.getByRole("button", { name: /expand sidebar/i });
    expect(button.getAttribute("aria-expanded")).toBe("false");
  });
});

describe("AppShell", () => {
  it("keeps navigation outside <main>, so skip-to-content skips it", () => {
    render(
      <AppShell sidebarCollapsed={false} topbar={<div>bar</div>}>
        <p>dashboard</p>
      </AppShell>,
    );
    const main = screen.getByRole("main");
    expect(main.textContent).toBe("dashboard");
    expect(main.contains(screen.getByRole("navigation"))).toBe(false);
  });
});

describe("useShellPreferences", () => {
  function Probe({ storage }: { storage: ReturnType<typeof fakeStorage> }) {
    const { layout, setLayout, sidebarCollapsed, toggleSidebar } = useShellPreferences(storage);
    return (
      <>
        <span data-testid="layout">{layout}</span>
        <span data-testid="collapsed">{String(sidebarCollapsed)}</span>
        <button type="button" onClick={() => setLayout("wall")}>
          wall
        </button>
        <button type="button" onClick={toggleSidebar}>
          toggle
        </button>
      </>
    );
  }

  it("starts from what was stored, on the first render", () => {
    const storage = fakeStorage({
      [LAYOUT_STORAGE_KEY]: "compact",
      [SIDEBAR_STORAGE_KEY]: "collapsed",
    });
    render(<Probe storage={storage} />);
    // Not "rows then compact after an effect": nobody should watch the
    // sidebar pop open and collapse again after paint.
    expect(screen.getByTestId("layout").textContent).toBe("compact");
    expect(screen.getByTestId("collapsed").textContent).toBe("true");
  });

  it("persists both preferences as they change", () => {
    const storage = fakeStorage();
    render(<Probe storage={storage} />);
    fireEvent.click(screen.getByRole("button", { name: "wall" }));
    fireEvent.click(screen.getByRole("button", { name: "toggle" }));
    expect(storage.seen.get(LAYOUT_STORAGE_KEY)).toBe("wall");
    expect(storage.seen.get(SIDEBAR_STORAGE_KEY)).toBe("collapsed");
  });
});

describe("useShellShortcuts", () => {
  function Probe({ onToggleSidebar, onEscape }: { onToggleSidebar: () => void; onEscape?: () => void }) {
    useShellShortcuts({ onToggleSidebar, onEscape });
    return <input aria-label="search" />;
  }

  it("toggles the sidebar on Ctrl+B and on Cmd+B", () => {
    const toggle = vi.fn();
    render(<Probe onToggleSidebar={toggle} />);
    fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    fireEvent.keyDown(window, { key: "b", metaKey: true });
    expect(toggle).toHaveBeenCalledTimes(2);
  });

  it("leaves Ctrl+B alone while someone is typing", () => {
    const toggle = vi.fn();
    render(<Probe onToggleSidebar={toggle} />);
    // Cmd+B in a text field means "bold" everywhere else on the web.
    fireEvent.keyDown(screen.getByLabelText("search"), { key: "b", ctrlKey: true });
    expect(toggle).not.toHaveBeenCalled();
  });

  it("toggles once while the chord is held down, not once per repeat", () => {
    const toggle = vi.fn();
    render(<Probe onToggleSidebar={toggle} />);
    // Holding Cmd/Ctrl+B fires keydown at the OS repeat rate. Toggling per
    // event flickers the sidebar and leaves it wherever the release lands.
    fireEvent.keyDown(window, { key: "b", ctrlKey: true });
    fireEvent.keyDown(window, { key: "b", ctrlKey: true, repeat: true });
    fireEvent.keyDown(window, { key: "b", ctrlKey: true, repeat: true });
    expect(toggle).toHaveBeenCalledTimes(1);
  });

  it("ignores a bare b", () => {
    const toggle = vi.fn();
    render(<Probe onToggleSidebar={toggle} />);
    fireEvent.keyDown(window, { key: "b" });
    expect(toggle).not.toHaveBeenCalled();
  });

  it("calls Escape only when there is something to leave", () => {
    const escape = vi.fn();
    const { unmount } = render(<Probe onToggleSidebar={() => {}} onEscape={escape} />);
    fireEvent.keyDown(window, { key: "Escape" });
    expect(escape).toHaveBeenCalledTimes(1);
    unmount();

    render(<Probe onToggleSidebar={() => {}} />);
    // No handler registered at all, so Esc keeps its browser meaning.
    expect(() => fireEvent.keyDown(window, { key: "Escape" })).not.toThrow();
  });
});
