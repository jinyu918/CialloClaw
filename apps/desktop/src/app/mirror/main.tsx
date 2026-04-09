import ReactDOM from "react-dom/client";
import { MirrorApp } from "@/features/mirror/MirrorApp";
import { AppProviders } from "@/features/shared/AppProviders";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <AppProviders>
    <MirrorApp />
  </AppProviders>,
);
