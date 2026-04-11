import { getCurrentWindow } from "@tauri-apps/api/window";

type HideOnCloseWindow = ReturnType<typeof getCurrentWindow>;

export function installHideOnCloseRequest(windowHandle: HideOnCloseWindow = getCurrentWindow()) {
  return windowHandle.onCloseRequested(async (event) => {
    event.preventDefault();
    await windowHandle.hide();
  });
}
