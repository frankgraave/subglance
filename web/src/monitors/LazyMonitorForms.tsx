import { lazy, Suspense } from "react";
import type { AddMonitorProps } from "./AddMonitor";
import type { EditMonitorFormProps } from "./EditMonitorForm";

/*
 * The add and edit monitor forms, loaded when a drawer first asks for one.
 *
 * Nobody needs either form until they press Add or Edit, yet both used to
 * load with the entry bundle, together with everything only they import: the
 * duration and channel pickers, the TLS floor, the JSON assertion and the
 * preview check. The bundle budget had been raised three times for those
 * forms; moving them behind `lazy()` is what buys the room back.
 *
 * Both loaders live in this one file so every caller shares one chunk and
 * one fallback. The fallback is a line of helper text inside the drawer's
 * own panel, the same as the maintenance schedule's: the drawer, its title
 * and its close button are on screen at once, and only the fields arrive a
 * moment later, so the frame does not move when they do.
 */
const AddMonitorChunk = lazy(() => import("./AddMonitor").then((module) => ({ default: module.AddMonitor })));
const EditMonitorFormChunk = lazy(() => import("./EditMonitorForm").then((module) => ({ default: module.EditMonitorForm })));

function FormLoading() {
  return <p className="field-help">Loading the form…</p>;
}

/** `AddMonitor`, fetched on first use. */
export function LazyAddMonitor(props: AddMonitorProps) {
  return <Suspense fallback={<FormLoading />}><AddMonitorChunk {...props} /></Suspense>;
}

/** `EditMonitorForm`, fetched on first use. */
export function LazyEditMonitorForm(props: EditMonitorFormProps) {
  return <Suspense fallback={<FormLoading />}><EditMonitorFormChunk {...props} /></Suspense>;
}
