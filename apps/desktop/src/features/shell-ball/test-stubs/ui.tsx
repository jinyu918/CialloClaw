import { createElement } from "react";

export function PanelSurface(props: Record<string, unknown>) {
  return createElement("div", props);
}

export function StatusBadge(props: Record<string, unknown>) {
  return createElement("span", props);
}
