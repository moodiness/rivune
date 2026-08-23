import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { setLocale } from "./i18n";
import "./styles.css";

setLocale(navigator.language);

const root = document.getElementById("root");
if (!root) throw new Error("Rivune TV root element is missing.");

createRoot(root).render(<StrictMode><App /></StrictMode>);
