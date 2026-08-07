type GuardedNavigation = {
  holds: number;
  state: unknown;
  url: string;
};

let guardedNavigation: GuardedNavigation | null = null;

function preventUnload(event: BeforeUnloadEvent) {
  event.preventDefault();
  event.returnValue = "";
}

export function acquireOneShotNavigationGuard(): () => void {
  if (!guardedNavigation) {
    guardedNavigation = {
      holds: 0,
      state: window.history.state,
      url: `${window.location.pathname}${window.location.search}${window.location.hash}`,
    };
    window.addEventListener("beforeunload", preventUnload);
  }
  guardedNavigation.holds += 1;
  let released = false;
  return () => {
    if (released || !guardedNavigation) return;
    released = true;
    guardedNavigation.holds -= 1;
    if (guardedNavigation.holds > 0) return;
    guardedNavigation = null;
    window.removeEventListener("beforeunload", preventUnload);
  };
}

export function restoreOneShotNavigation(): boolean {
  if (!guardedNavigation) return false;
  const currentURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (currentURL === guardedNavigation.url) return false;
  window.history.pushState(guardedNavigation.state, "", guardedNavigation.url);
  return true;
}
