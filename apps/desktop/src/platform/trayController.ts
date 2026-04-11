import { openOrFocusDesktopWindow } from "@/platform/windowController";

// 该文件封装托盘入口控制能力。
export function openControlPanelFromTray() {
  return openOrFocusDesktopWindow("control-panel");
}
