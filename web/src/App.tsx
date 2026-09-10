import { useTheme } from "./theme/useTheme";
import { ThemeToggle } from "./components/ThemeToggle";
import { TokenSheet } from "./components/TokenSheet";

/**
 * Placeholder shell. The real dashboard arrives with SUB-46/47; for now this
 * renders the token sheet, which is what makes the tokens reviewable instead
 * of merely present.
 */
export default function App() {
  const { preference, resolved, setPreference } = useTheme();

  return (
    <div className="min-h-dvh bg-canvas text-ink transition-colors">
      <header className="border-b border-border">
        <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-4">
          <div>
            <h1 className="text-[21px] font-medium tracking-[-0.02em]">SubGlance</h1>
            <p className="text-[12.5px] text-ink-3">
              Design tokens — {resolved} theme
            </p>
          </div>
          <ThemeToggle preference={preference} onChange={setPreference} />
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-6 py-8">
        <TokenSheet />
      </main>
    </div>
  );
}
