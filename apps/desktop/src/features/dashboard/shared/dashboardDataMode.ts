import { loadStoredValue, saveStoredValue } from "@/platform/storage";

export type DashboardDataMode = "rpc" | "mock";
export type DashboardDataModePage = "tasks" | "notes" | "memory" | "safety";

function getDashboardDataModeStorageKey(page: DashboardDataModePage) {
  return `dashboard:data-mode:${page}`;
}

export function loadDashboardDataMode(page: DashboardDataModePage): DashboardDataMode {
  try {
    const storedValue = loadStoredValue<DashboardDataMode>(getDashboardDataModeStorageKey(page));
    return storedValue === "mock" ? "mock" : "rpc";
  } catch {
    return "rpc";
  }
}

export function saveDashboardDataMode(page: DashboardDataModePage, mode: DashboardDataMode) {
  try {
    saveStoredValue(getDashboardDataModeStorageKey(page), mode);
  } catch {
    return;
  }
}
