// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { act } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient } from "@tanstack/react-query";
import { LiveMonitorsRoot } from "./LiveMonitors";
import { inventoryFromApi } from "./inventory";
import { confirmLeave, confirmNavigation, registerLeaveGuard, registerNavigationCleanup } from "../shell/leaveGuard";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it('navigation invalidates a pending edit load rather than hiding its eventual drawer', async () => {
  const monitor = inventoryFromApi({ id: 1, name: 'auth', type: 'http', target: 'https://auth.example', created_at: '2026-09-01T00:00:00Z', enabled: true, status: 'up', interval_s: 60, timeout_s: 10 });
  let finish!: (value: { monitor: typeof monitor; etag: string }) => void;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  render(<LiveMonitorsRoot client={client} fetchMonitors={async () => [monitor]} forEdit={() => new Promise((resolve) => { finish = resolve; })} />);
  fireEvent.click(await screen.findByRole('button', { name: 'Edit auth' }));
  act(() => { expect(confirmNavigation()).toBe(true); });
  await act(async () => { finish({ monitor, etag: 'W/"1757606400"' }); });
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('rejection preserves ownership and guard; scoped dismissal never notifies navigation owners', () => {
  let dirty = true;
  const element = document.createElement('form');
  const close = vi.fn();
  const unregister = registerNavigationCleanup(close);
  const unguard = registerLeaveGuard({ element: () => element, dirty: () => dirty, discard: () => { dirty = false; } });
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  try {
    expect(confirmNavigation()).toBe(false);
    expect(dirty).toBe(true);
    expect(close).not.toHaveBeenCalled();
    confirm.mockReturnValue(true);
    expect(confirmLeave(element)).toBe(true);
    expect(close).not.toHaveBeenCalled();
    expect(confirmNavigation()).toBe(true);
    expect(close).toHaveBeenCalledTimes(1);
    unregister();
    expect(confirmNavigation()).toBe(true);
    expect(close).toHaveBeenCalledTimes(1);
    expect(confirm).toHaveBeenCalledTimes(2);
  } finally { unregister(); unguard(); }
});
