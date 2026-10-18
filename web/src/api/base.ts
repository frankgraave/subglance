/**
 * Where this instance is served from: "" at the root of a host, "/subglance"
 * behind a proxy that serves it under that path.
 *
 * Read from the bundle's own address rather than configured. Every chunk is
 * built into `assets/` directly under the place the dashboard is served from
 * (vite.config.ts), so the directory above this module's URL is that place,
 * whatever the proxy in front calls it. The server cannot say: behind a proxy
 * that strips the prefix, it never sees one.
 *
 * Under the dev server and in tests the module is not a built chunk in an
 * `assets/` directory, so the base is the root.
 */
export function baseFrom(moduleURL: string): string {
  const url = new URL(".", moduleURL);
  if (url.protocol !== "http:" && url.protocol !== "https:") return "";
  const dir = url.pathname;
  return dir.endsWith("/assets/") ? dir.slice(0, -"/assets/".length) : "";
}

export const APP_BASE = baseFrom(import.meta.url);

/** `path` (an absolute path in the app, such as `/monitors`) as the browser addresses it. */
export function withBase(base: string, path: string): string {
  return base + path;
}

/**
 * The app's own path for a pathname the browser shows. A pathname outside the
 * base is returned as it is, so the router names it as a page it does not have
 * rather than mistaking it for one it does.
 */
export function withoutBase(base: string, pathname: string): string {
  if (base === "" || !pathname.startsWith(base)) return pathname;
  const rest = pathname.slice(base.length);
  return rest === "" ? "/" : rest.startsWith("/") ? rest : pathname;
}

/** A link, history entry or request URL for an app path. */
export const appPath = (path: string) => withBase(APP_BASE, path);

/** The route path of the address in the browser's location bar. */
export const routePathname = (pathname: string) => withoutBase(APP_BASE, pathname);
