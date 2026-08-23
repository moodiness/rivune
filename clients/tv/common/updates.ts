import { useEffect, useState } from "react";
import type { TvUpdateState } from "../update-api";

const unavailable: TvUpdateState = Object.freeze({
  status: "unavailable",
  currentVersion: __RIVUNE_VERSION__,
});

function updaterState(): TvUpdateState {
  return window.RivuneUpdater?.getState() ?? unavailable;
}

export function useTvUpdateState(): TvUpdateState {
  const [state, setState] = useState(updaterState);
  useEffect(() => {
    const updater = window.RivuneUpdater;
    if (!updater) return;
    setState(updater.getState());
    return updater.subscribe(() => setState(updater.getState()));
  }, []);
  return state;
}

export function checkForTvUpdate(): void {
  void window.RivuneUpdater?.checkManually();
}

export function downloadTvUpdate(): void {
  void window.RivuneUpdater?.download();
}

export function restartForTvUpdate(): void {
  void window.RivuneUpdater?.restart();
}
