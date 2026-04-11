import { Window } from "@tauri-apps/api/window";

export type DesktopWindowLabel = "dashboard" | "control-panel";
export type WindowRouteLabel = "dashboard" | "safety";

const windowRouteTargets: Record<WindowRouteLabel, string> = {
  dashboard: "./dashboard.html",
  safety: "./dashboard.html#/safety",
};

// 该文件封装桌面窗口控制能力。
export async function openOrFocusDesktopWindow(label: DesktopWindowLabel) {
  const windowHandle = await Window.getByLabel(label);

  if (windowHandle === null) {
    throw new Error(`Desktop window not found: ${label}`);
  }

  await windowHandle.show();
  await windowHandle.setFocus();

  return label;
}

// openWindowRoute 处理当前模块的相关逻辑。
export function openWindowRoute(label: WindowRouteLabel) {
  if (typeof window !== "undefined") {
    window.location.assign(windowRouteTargets[label]);
  }

  return label;
}
