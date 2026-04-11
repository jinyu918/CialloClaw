// 该入口负责挂载仪表盘窗口。
import ReactDOM from "react-dom/client";
import { AppProviders } from "@/features/shared/AppProviders";
import { installHideOnCloseRequest } from "@/platform/hideOnCloseRequest";
import { DashboardRoot } from "./DashboardRoot";

void installHideOnCloseRequest();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <AppProviders>
    <DashboardRoot />
  </AppProviders>,
);
