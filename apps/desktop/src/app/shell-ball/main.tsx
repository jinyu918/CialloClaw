// 该入口负责挂载悬浮球窗口。
import ReactDOM from "react-dom/client";
import { AppProviders } from "@/features/shared/AppProviders";
import { ShellBallApp } from "@/features/shell-ball/ShellBallApp";
import "@/features/shell-ball/shellBall.css";

document.documentElement.setAttribute("data-shell-ball-app", "true");
document.body.setAttribute("data-shell-ball-app", "true");

ReactDOM.createRoot(document.getElementById("root")!).render(
  <AppProviders>
    <ShellBallApp isDev={import.meta.env.DEV} />
  </AppProviders>,
);
