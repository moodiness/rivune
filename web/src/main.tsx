/// <reference types="vite/client" />

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { AuthProvider } from "./auth";
import { ApplicationsPage } from "./pages/Applications";
import { setLocale } from "./i18n";
import { NotificationViewport } from "./notifications";
import "./styles.css";

await setLocale();

const basePath = import.meta.env.BASE_URL;
const relativePath = window.location.pathname.startsWith(basePath)
  ? window.location.pathname.slice(basePath.length).replace(/^\/+|\/+$/g, "")
  : window.location.pathname.replace(/^\/+|\/+$/g, "");
const applicationsRoute = basePath !== "/" || relativePath === "apps";
if (applicationsRoute) {
  document.title = "Rivune applications";
  document.querySelector('meta[name="description"]')?.setAttribute("content", "Download verified Rivune applications for Android, Apple, and Windows.");
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {applicationsRoute
      ? <ApplicationsPage />
      : <AuthProvider><App /><NotificationViewport /></AuthProvider>}
  </StrictMode>,
);
