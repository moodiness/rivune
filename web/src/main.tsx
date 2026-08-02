import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { AuthProvider } from "./auth";
import { setLocale } from "./i18n";
import { NotificationViewport } from "./notifications";
import "./styles.css";

setLocale();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider><App /><NotificationViewport /></AuthProvider>
  </StrictMode>,
);
