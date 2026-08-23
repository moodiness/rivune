"use strict";

const token = location.pathname.split("/").filter(Boolean)[0];
const base = `/${token}/`;
const api = (path, options = {}) => fetch(`${base}${path}`, {
  ...options,
  headers: {
    "Content-Type": "application/json",
    "X-Rivune-Token": token,
    ...options.headers,
  },
}).then(async (response) => {
  const text = await response.text();
  if (!response.ok) throw new Error(text.trim() || `HTTP ${response.status}`);
  return text ? JSON.parse(text) : null;
});

const state = { platform: "webos", release: null, busy: false };
const elements = {
  summary: document.querySelector("#summary"),
  release: document.querySelector("#release"),
  digest: document.querySelector("#digest"),
  form: document.querySelector("#installer"),
  ip: document.querySelector("#ip"),
  device: document.querySelector("#device-name"),
  passphrase: document.querySelector("#passphrase"),
  profile: document.querySelector("#profile"),
  webos: document.querySelector("#webos-fields"),
  tizen: document.querySelector("#tizen-fields"),
  hostIP: document.querySelector("#host-ip"),
  log: document.querySelector("#log"),
  version: document.querySelector("#version"),
};

function log(message) {
  elements.log.textContent += `\n${message}`;
  elements.log.scrollTop = elements.log.scrollHeight;
}

function render() {
  document.querySelectorAll("nav button").forEach((button) => {
    button.classList.toggle("active", button.dataset.platform === state.platform);
  });
  elements.webos.hidden = state.platform !== "webos";
  elements.tizen.hidden = state.platform !== "tizen";
  const selectedPackage = state.release?.[state.platform];
  elements.release.textContent = state.release ? `${state.release.tagName} · ${selectedPackage.name}` : "Unavailable";
  elements.digest.textContent = selectedPackage ? `SHA-256 ${selectedPackage.sha256}` : "";
  document.querySelectorAll("input").forEach((control) => { control.disabled = state.busy; });
  document.querySelectorAll("button[data-action]").forEach((control) => {
    control.disabled = state.busy || (control.dataset.action === "install" && !state.release);
  });
}

async function initialize() {
  let status;
  try {
    status = await api("api/status");
    elements.version.textContent = `Companion ${status.version}`;
    elements.hostIP.textContent = status.localIps.join(" or ") || "unavailable";
    if (status.webosDevices[0]) {
      elements.device.value = status.webosDevices[0].name;
      elements.ip.value = status.webosDevices[0].host || "";
    }
  } catch (error) {
    elements.summary.textContent = "The local companion status is unavailable.";
    log(error.message);
    render();
    return;
  }

  try {
    state.release = await api("api/release");
    elements.summary.textContent = `Verified ${state.release.tagName}. webOS tools: ${status.tools.webos ? "ready" : "missing"}. Tizen tools: ${status.tools.tizen ? "ready" : "missing"}.`;
  } catch (error) {
    elements.summary.textContent = `Companion ready. webOS tools: ${status.tools.webos ? "ready" : "missing"}. Tizen tools: ${status.tools.tizen ? "ready" : "missing"}. The latest release is not yet compatible.`;
    log(error.message);
  }
  render();
}

async function run(action) {
  if (state.busy) return;
  state.busy = true;
  render();
  log(`Starting ${action} for ${state.platform}…`);
  try {
    const result = await api("api/run", {
      method: "POST",
      body: JSON.stringify({
        platform: state.platform,
        action,
        ip: elements.ip.value,
        deviceName: elements.device.value,
        passphrase: elements.passphrase.value,
        profile: elements.profile.value,
      }),
    });
    elements.passphrase.value = "";
    for (const line of result.logs || []) log(line);
    if (!result.ok) throw new Error(result.error || "Operation failed");
    log(`${action} completed.`);
  } catch (error) {
    elements.passphrase.value = "";
    log(`ERROR: ${error.message}`);
  } finally {
    state.busy = false;
    render();
  }
}

document.querySelectorAll("nav button").forEach((button) => {
  button.addEventListener("click", () => {
    state.platform = button.dataset.platform;
    render();
  });
});
elements.form.addEventListener("submit", (event) => {
  event.preventDefault();
  void run("install");
});
document.querySelectorAll("button[data-action]").forEach((button) => {
  button.addEventListener("click", () => void run(button.dataset.action));
});
document.querySelector("#clear").addEventListener("click", () => { elements.log.textContent = "Ready."; });
document.querySelector("#close").addEventListener("click", async () => {
  try {
    await api("api/close", { method: "POST", body: "{}" });
    document.body.innerHTML = "<main><h1>Companion closed.</h1><p>You can close this tab.</p></main>";
  } catch (error) {
    log(error.message);
  }
});

render();
void initialize();
