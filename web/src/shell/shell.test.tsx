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
  it("marks the screen you are on as the current page", () => {
    render(<Sidebar collapsed={false} />);
    const current = screen.getByText("Dashboard").closest("[aria-current]");
    expect(current).toBeTruthy();
  });

  it("marks incidents as current when that is the screen you are on", () => {
    render(<Sidebar collapsed={false} current="incidents" />);
    expect(screen.getByText("Incidents").closest("[aria-current]")).toBeTruthy();
    expect(screen.getByText("Dashboard").closest("[aria-current]")).toBeNull();
  });

  it("keeps the dashboard lit while a monitor's detail view is open", () => {
    // A monitor belongs to the dashboard branch of the product. Lighting
    // nothing would leave the rail claiming you are nowhere.
    render(<Sidebar collapsed={false} current="monitor" />);
    expect(screen.getByText("Dashboard").closest("[aria-current]")).toBeTruthy();
  });

  it("does not promise the two screens that do not exist", () => {
    render(<Sidebar collapsed={false} />);
    // They are shown — the shape of the product is information — but never as
    // something you can press, and never with a fabricated count beside them.
    for (const label of ["Notifications", "Settings"]) {
      const item = screen.getByText(label).closest(".shell-nav-item")!;
      expect(item.getAttribute("data-state")).toBe("planned");
      expect(item.querySelector("a, button")).toBeNull();
    }
    expect(screen.getAllByText("Soon")).toHaveLength(2);
  });

  it("makes monitors a real link now that the screen exists (SUB-122)", () => {
    // The most misleading "Soon" left: the dashboard is full of monitors, so
    // an item called Monitors that leads nowhere reads as a broken link to the
    // screen already on display.
    render(<Sidebar collapsed={false} />);
    const link = screen.getByText("Monitors").closest("a");
    expect(link?.getAttribute("href")).toBe("/monitors");
    expect(
      screen
        .getByText("Monitors")
        .closest(".shell-nav-item")
        ?.querySelector(".shell-nav-soon"),
      "a built destination must not still say Soon",
    ).toBeNull();
  });

  it("marks monitors as current when that is the screen you are on", () => {
    render(<Sidebar collapsed={false} current="monitors" />);
    expect(screen.getByText("Monitors").closest("[aria-current]")).toBeTruthy();
    expect(screen.getByText("Dashboard").closest("[aria-current]")).toBeNull();
  });

  it("makes incidents a real link now that the screen exists (SUB-34)", () => {
    // The oldest of the "Soon" promises, and the one the product could least
    // afford to keep breaking: the endpoint behind it has existed since the
    // backend landed. A real <a href>, so it can be middle-clicked, copied
    // and pasted into a chat window like any destination.
    render(<Sidebar collapsed={false} />);
    const link = screen.getByText("Incidents").closest("a");
    expect(link?.getAttribute("href")).toBe("/incidents");
    expect(
      screen.getByText("Incidents").closest(".shell-nav-item")
        ?.querySelector(".shell-nav-soon"),
      "a built destination must not still say Soon",
    ).toBeNull();
  });

  it("carries no incident count, because the count belongs on the screen", () => {
    // DESIGN.md §12 records why the fabricated "2 incidents" badge was
    // removed: on a monitoring tool an invented number is indistinguishable
    // from a real alert. A real number here would be a second source of
    // truth for the length of a list this component has never seen.
    render(<Sidebar collapsed={false} />);
    const item = screen.getByText("Incidents").closest(".shell-nav-item")!;
    expect(item.textContent).toBe("Incidents");
  });

  it("keeps the labels readable to a screen reader when collapsed", () => {
    render(<Sidebar collapsed />);
    // Collapsed is a rail, not a disappearance: the text stays in the DOM so
    // the icons are not five unnamed squares.
    expect(screen.getByText("Dashboard")).toBeTruthy();
    expect(
      document.querySelector('.shell-sidebar[data-collapsed="true"]'),
    ).toBeTruthy();
  });
});

describe("LayoutSwitcher", () => {
  it("offers all four layouts and reports the pressed one", () => {
    render(<LayoutSwitcher layout="compact" onChange={() => {}} />);
    const buttons = screen.getAllByRole("button");
    expect(buttons.map((b) => b.textContent)).toEqual([
      "Rows",
      "Cards",
      "Compact",
      "Status wall",
    ]);
    expect(
      screen
        .getByRole("button", { name: "Compact" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
  });

  it("shows the layout actually rendered, not the overridden preference", () => {
    // A toolbar claiming Rows while cards are on screen is worse than one
    // admitting the narrow viewport won.
    render(
      <LayoutSwitcher layout="rows" effective="cards" onChange={() => {}} />,
    );
    expect(
      screen
        .getByRole("button", { name: "Cards" })
        .getAttribute("aria-pressed"),
    ).toBe("true");
    expect(
      screen.getByRole("button", { name: "Rows" }).getAttribute("aria-pressed"),
    ).toBe("false");
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
    const button = screen.getByRole("button", {
      name: /collapse sidebar \(ctrl\+b\)/i,
    });
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
    const { layout, setLayout, sidebarCollapsed, toggleSidebar } =
      useShellPreferences(storage);
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
  function Probe({
    onToggleSidebar,
    onEscape,
  }: {
    onToggleSidebar: () => void;
    onEscape?: () => void;
  }) {
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
    fireEvent.keyDown(screen.getByLabelText("search"), {
      key: "b",
      ctrlKey: true,
    });
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
    const { unmount } = render(
      <Probe onToggleSidebar={() => {}} onEscape={escape} />,
    );
    fireEvent.keyDown(window, { key: "Escape" });
    expect(escape).toHaveBeenCalledTimes(1);
    unmount();

    render(<Probe onToggleSidebar={() => {}} />);
    // No handler registered at all, so Esc keeps its browser meaning.
    expect(() => fireEvent.keyDown(window, { key: "Escape" })).not.toThrow();
  });
});

describe("the topbar holds only what is true on every screen", () => {
  /*
   * The defect this closes, measured before it was fixed.
   *
   * The card-columns control shipped in this bar for one commit. It exists
   * only for Cards, so switching to Rows removed four buttons from a
   * right-aligned group — and because the group is right-aligned, what moved
   * was everything *before* the gap: the add button and the layout switcher
   * itself, by 121px. The control the user had just clicked slid out from
   * under the cursor.
   *
   * View-specific tools belong to the dashboard's own tools row, where
   * appearing and disappearing costs nothing above them. jsdom has no layout
   * so the 121px cannot be re-measured here; what *can* be asserted is the
   * structural rule that produced it — this bar renders the same controls
   * whichever layout is current.
   */
  const bar = (layout: "rows" | "cards") =>
    render(
      <Topbar
        sidebarCollapsed={false}
        onToggleSidebar={() => {}}
        layout={layout}
        effectiveLayout={layout}
        onLayoutChange={() => {}}
        themePreference="dark"
        onThemeChange={() => {}}
        workbenchOpen={false}
        onToggleWorkbench={() => {}}
      />,
    );

  const groupNames = () =>
    screen.getAllByRole("group").map((g) => g.getAttribute("aria-label"));

  it("renders the same control groups in every layout", () => {
    const { unmount } = bar("cards");
    const inCards = groupNames();
    unmount();

    bar("rows");
    expect(
      groupNames(),
      "a group that comes and goes here shifts every control before it",
    ).toEqual(inCards);
  });

  it("does not host the cards-per-row control", () => {
    bar("cards");
    expect(
      screen.queryByRole("group", { name: "Cards per row" }),
      "view tools belong to the dashboard's tools row, not the shell",
    ).toBeNull();
  });
});
