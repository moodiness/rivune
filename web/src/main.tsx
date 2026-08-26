/// <reference types="vite/client" />

import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { AccessibilityProvider } from "./accessibility";
import { AuthProvider, useAuth } from "./auth";
import { ApplicationsPage } from "./pages/Applications";
import { setLocale, translate as t } from "./i18n";
import { NotificationViewport } from "./notifications";
import "./styles.css";


const basePath = import.meta.env.BASE_URL;
const relativePath = window.location.pathname.startsWith(basePath)
  ? window.location.pathname.slice(basePath.length).replace(/^\/+|\/+$/g, "")
  : window.location.pathname.replace(/^\/+|\/+$/g, "");
const applicationsRoute = basePath !== "/" || relativePath === "apps";
const requestedApplicationsLocale = applicationsRoute ? new URLSearchParams(window.location.search).get("lang") : null;
await setLocale(requestedApplicationsLocale);
if (applicationsRoute) {
  document.title = t("applications.meta.title");
  document.querySelector('meta[name="description"]')?.setAttribute("content", t("applications.meta.description"));
}

function ProfileExperience() {
  const { activeProfile } = useAuth();
  return <AccessibilityProvider profileId={activeProfile?.id}><App /><NotificationViewport /></AccessibilityProvider>;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {applicationsRoute
      ? <ApplicationsPage />
      : <AuthProvider><ProfileExperience /></AuthProvider>}
  </StrictMode>,
);
