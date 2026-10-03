import { useEffect, useId, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { useQuery, type QueryClient } from "@tanstack/react-query";
import { fetchInventory, inventoryQueryKey, setMonitorPaused } from "../monitors/inventoryApi";
import type { ThemePreference } from "../theme/theme";
import type { NavRoute } from "../shell/Sidebar";
import { PAGE_TITLES } from "../shell/pages";
import { SearchIcon } from "../shell/icons";
import { Led } from "../monitors/Led";
import { statusWord } from "../monitors/format";
import type { MonitorStatus } from "../monitors/types";

export type CommandMenuProps = {
  client: QueryClient;
  open?: boolean;
  canWrite: boolean;
  onClose: () => void;
  onOpenMonitor: (id: string) => void;
  onNavigate: (route: NavRoute) => void;
  onAddMonitor: () => void;
  onThemeChange: (preference: ThemePreference) => void;
};

/**
 * The headings the results fall under, in the order they are drawn.
 *
 * Monitors first: past a handful of monitors, finding one is what the menu is
 * opened for (DESIGN.md §7.7). The arrow keys walk the flat list in this same
 * order, so the active option never jumps backwards across a heading.
 */
const GROUPS = ["Monitors", "Actions", "Navigation", "Theme"] as const;
type Group = (typeof GROUPS)[number];
type Command = { id: string; group: Group; label: string; detail?: string; status?: MonitorStatus; keepOpen?: boolean; run: () => void };

/** Native top-layer dialog: it stays above drawers without another z-index. */
export function CommandMenu({ client, open = true, canWrite, onClose, onOpenMonitor, onNavigate, onAddMonitor, onThemeChange }: CommandMenuProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const composing = useRef(false);
  const id = useId();
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [wasOpen, setWasOpen] = useState(open);
  if (wasOpen !== open) {
    setWasOpen(open);
    setQuery("");
    setSelected(0);
    setError("");
  }
  const pending = useRef<AbortController | null>(null);
  // App keeps this owner mounted while the session lives. Dismissal must not
  // abort a write the server may have committed; session teardown must.
  useEffect(() => () => { pending.current?.abort(); }, []);
  const inventory = useQuery({ queryKey: inventoryQueryKey, queryFn: ({ signal }) => fetchInventory(signal), enabled: open, staleTime: 0, retry: false }, client);
  async function changePaused(id: string, paused: boolean) {
    if (!canWrite || pending.current) return;
    const controller = new AbortController();
    pending.current = controller;
    setError("");
    setBusy(paused ? "Pausing monitor…" : "Resuming monitor…");
    try {
      await setMonitorPaused(id, paused, controller.signal);
      if (controller.signal.aborted) return;
      await Promise.all([
        client.invalidateQueries({ queryKey: ["monitors"] }),
        client.invalidateQueries({ queryKey: ["monitor-detail", id] }),
      ]);
    } catch (failure) {
      if (!controller.signal.aborted) setError(failure instanceof Error ? failure.message : "Could not update monitor. Try again.");
    } finally {
      if (!controller.signal.aborted) { pending.current = null; setBusy(""); }
    }
  }
  /*
   * A monitor's row says how it is doing, in a word as well as a lamp: the
   * word is what a screen reader hears and what a colour-blind reader reads
   * (DESIGN.md §2.3), and because it is part of the searched text, typing
   * "down" narrows the list to the monitors that are.
   */
  const commands: Command[] = [
    ...(inventory.data ?? []).map((m) => ({ id: `open-${m.id}`, group: "Monitors" as const, label: `Open ${m.name}`, detail: `${statusWord(m.status)} \u00b7 ${m.target}`, status: m.status, run: () => onOpenMonitor(m.id) })),
    ...(canWrite ? [
      ...(inventory.data ?? []).map((m) => ({ id: `pause-${m.id}`, group: "Actions" as const, label: `${m.enabled ? "Pause" : "Resume"} ${m.name}`, keepOpen: true, run: () => { void changePaused(m.id, m.enabled); } })),
      { id: "add", group: "Actions" as const, label: "Add monitor", run: onAddMonitor },
    ] : []),
    ...(["dashboard", "monitors", "incidents", "notifications", "settings"] as const).map((route) => ({ id: route, group: "Navigation" as const, label: `Go to ${PAGE_TITLES[route]}`, run: () => onNavigate(route) })),
    ...(["light", "dark", "system"] as const).map((theme) => ({ id: theme, group: "Theme" as const, label: `Use ${theme} theme`, run: () => onThemeChange(theme) })),
  ];
  const needle = query.trim().toLowerCase();
  const results = GROUPS.flatMap((group) => commands.filter((c) => c.group === group && `${c.label} ${c.detail ?? ""}`.toLowerCase().includes(needle)));
  const index = Math.min(selected, Math.max(0, results.length - 1));
  const activeId = results[index]?.id;
  useEffect(() => {
    if (!open) return;
    const opener = document.activeElement;
    const dialog = dialogRef.current!;
    dialog.showModal();
    inputRef.current?.focus();
    return () => {
      dialog.close();
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
    };
  }, [open]);
  useEffect(() => {
    dialogRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: "nearest" });
  }, [activeId, open]);
  function activate(i: number) {
    const command = results[i];
    if (!command || pending.current) return;
    if (command.keepOpen) inputRef.current?.focus();
    else onClose();
    command.run();
  }
  function onKeyDown(event: KeyboardEvent<HTMLDialogElement>) {
    // Only the launch chord may reach the shell behind the topmost modal.
    if (!(event.key.toLowerCase() === "k" && (event.ctrlKey || event.metaKey))) event.stopPropagation();
    if (event.nativeEvent.isComposing || event.keyCode === 229 || composing.current) {
      if (event.key === "Escape" || event.key === "Enter" || event.key === " ") event.preventDefault();
      return;
    }
    if (event.key === "Tab") {
      const nodes = [...dialogRef.current!.querySelectorAll<HTMLElement>('input, button:not([tabindex="-1"])')];
      const first = nodes[0], last = nodes[nodes.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
      return;
    }
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey || (event.repeat && (event.key === "Enter" || event.key === "Escape"))) {
      if (event.key === "Escape" || event.key === "Enter" || event.key === " ") event.preventDefault();
      return;
    }
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); onClose(); }
    if ((event.key === "ArrowDown" || event.key === "ArrowUp") && event.target === inputRef.current) {
      event.preventDefault();
      setSelected((index + (event.key === "ArrowDown" ? 1 : -1) + results.length) % (results.length || 1));
    }
    if (event.key === "Enter" && event.target === inputRef.current) { event.preventDefault(); activate(index); }
  }
  if (!open) return null;
  return (
    <dialog ref={dialogRef} className="command-menu" aria-label="Command menu" onKeyDown={onKeyDown} onCancel={(e) => { e.preventDefault(); if (!composing.current) onClose(); }}>
      {/* The field's glyph sits inside the input's box rather than beside it,
          so the focus ring drawn on the input goes round both. */}
      <div className="command-search">
        <SearchIcon className="command-search-icon" />
        <input ref={inputRef} role="combobox" aria-label="Search monitors or commands" placeholder="Search monitors or commands…" aria-expanded="true" aria-controls={`${id}-results`} aria-activedescendant={results.length ? `${id}-${index}` : undefined} aria-autocomplete="list" value={query} onChange={(e) => { setQuery(e.target.value); setSelected(0); }} onCompositionStart={() => { composing.current = true; }} onCompositionEnd={() => { composing.current = false; }} />
      </div>
      {inventory.isPending && <p role="status">Loading monitors…</p>}
      {inventory.isError && <div><p role="alert">{inventory.error.message}</p><button type="button" onClick={() => { void inventory.refetch(); }}>Retry loading monitors</button></div>}
      {!inventory.isPending && !inventory.isError && inventory.data.length === 0 && !query && <p role="status">No monitors yet. Navigate or add your first monitor.</p>}
      {results.length === 0 && !inventory.isPending && <p role="status">No matching commands or monitors.</p>}
      {busy && <p role="status">{busy}</p>}
      {error && <p role="alert">{error}</p>}
      {/*
       * Grouped the way the ARIA practices' grouped listbox is: a `group` per
       * heading, named by a presentational label inside it. The options keep
       * one flat index across the groups, so aria-activedescendant and the
       * arrow keys do not need to know the headings exist.
       */}
      <div id={`${id}-results`} role="listbox" aria-label="Commands" tabIndex={-1}>
        {GROUPS.map((group) => {
          const rows = results.flatMap((c, i) => (c.group === group ? [{ c, i }] : []));
          if (rows.length === 0) return null;
          return (
            <div role="group" aria-labelledby={`${id}-${group}`} key={group}>
              <div role="presentation" id={`${id}-${group}`} className="command-group">{group}</div>
              {rows.map(({ c, i }) => (
                <button type="button" role="option" tabIndex={-1} id={`${id}-${i}`} aria-selected={i === index} aria-disabled={!!busy} key={c.id} className={c.status ? "command-monitor" : undefined} onClick={() => activate(i)}>
                  {c.status && <Led status={c.status} labelled={false} className="command-lamp" />}
                  <span className="command-label">{c.label}</span>
                  {/* The space keeps the two lines apart in the option's
                      accessible name; the grid does not draw it. */}
                  {c.detail && <>{" "}<span className="command-detail">{c.detail}</span></>}
                </button>
              ))}
            </div>
          );
        })}
      </div>
      {/*
       * The keys the menu answers to, where the menu is. Hidden from assistive
       * technology because the combobox role already announces how it is
       * driven, and hidden on a phone because there is no keyboard to press.
       */}
      <div className="command-foot">
        <span className="command-keys" aria-hidden="true">
          <span><kbd>{"\u2191"}</kbd><kbd>{"\u2193"}</kbd> to move</span>
          <span><kbd>Enter</kbd> to select</span>
        </span>
        <button type="button" onClick={onClose} aria-label="Close command menu" aria-keyshortcuts="Escape">
          Close <kbd aria-hidden="true">Esc</kbd>
        </button>
      </div>
    </dialog>
  );
}
