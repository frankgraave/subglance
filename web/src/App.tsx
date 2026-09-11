import { useState } from "react";
import { useTheme } from "./theme/useTheme";
import { ThemeToggle } from "./components/ThemeToggle";
import { TokenSheet } from "./components/TokenSheet";
import { HeartbeatGallery } from "./heartbeat/Gallery";
import { DashboardWorkbench } from "./monitors/Workbench";

/**
 * Component workbench. Not the product: the dashboard itself arrives with
 * SUB-22. This shell exists so the pieces the dashboard will be built from can
 * be judged in isolation, in both themes, before they are wired to real data.
 */

const TABS = [
  { id: "dashboard", label: "Dashboard" },
  { id: "heartbeat", label: "Heartbeat bar" },
  { id: "tokens", label: "Design tokens" },
] as const;

type Tab = (typeof TABS)[number]["id"];

export default function App() {
  const { preference, resolved, setPreference } = useTheme();
  const [tab, setTab] = useState<Tab>("dashboard");

  return (
    <div className="min-h-dvh bg-canvas text-ink transition-colors">
      <header className="border-b border-border">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-3 px-4 py-4 sm:px-6">
          <div>
            <h1 className="text-[19px] font-medium tracking-[-0.02em] sm:text-[21px]">SubGlance</h1>
            <p className="text-[12.5px] text-ink-3">Component workbench — {resolved} theme</p>
          </div>
          <ThemeToggle preference={preference} onChange={setPreference} />
        </div>
        <div className="mx-auto flex max-w-5xl gap-1 overflow-x-auto px-4 sm:px-6">
          {TABS.map((t) => (
            <button
              key={t.id}
              type="button"
              onClick={() => setTab(t.id)}
              aria-current={tab === t.id ? "page" : undefined}
              className={`-mb-px border-b-2 px-3 py-2 text-[13px] transition-colors ${
                tab === t.id
                  ? "border-ink text-ink"
                  : "border-transparent text-ink-3 hover:text-ink-2"
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>
      </header>
      <main className={`mx-auto px-4 py-8 sm:px-6 ${tab === "dashboard" ? "max-w-6xl" : "max-w-5xl"}`}>
        {tab === "dashboard" && <DashboardWorkbench />}
        {tab === "heartbeat" && <HeartbeatGallery />}
        {tab === "tokens" && <TokenSheet />}
      </main>
    </div>
  );
}
