import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { AuthProvider } from "./auth";
import { locale } from "./i18n";
import { NotificationViewport } from "./notifications";
import "./styles.css";

document.documentElement.lang = locale;

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthProvider><App /><NotificationViewport /></AuthProvider>
  </StrictMode>,
);
