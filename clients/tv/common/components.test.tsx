import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ErrorPanel, Modal, TvButton } from "./components";
import { installSpatialNavigation } from "./focus";

function ModalHarness() {
  const [open, setOpen] = useState(false);
  return <div><TvButton data-x="10" onClick={() => setOpen(true)}>Open</TvButton>{open && <Modal title="Tracks" onClose={() => setOpen(false)}><TvButton data-x="20">First track</TvButton><TvButton data-x="30">Second track</TvButton></Modal>}</div>;
}

describe("TV modal focus", () => {
  let container: HTMLDivElement;
  let root: Root;
  let removeNavigation: () => void;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
      const left = Number(this.dataset.x ?? 0);
      return { x: left, y: 0, left, top: 0, right: left + 10, bottom: 10, width: 10, height: 10, toJSON: () => ({}) };
    });
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: vi.fn() });
    container = document.createElement("div");
    document.body.append(container);
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => {
      callback(0);
      return 1;
    });
    root = createRoot(container);
    removeNavigation = installSpatialNavigation(vi.fn());
  });

  afterEach(async () => {
    removeNavigation();
    await act(async () => root.unmount());
    container.remove();
  });

  it("inerts the background, traps Tab and arrows, closes with Escape, then restores its invoker", async () => {
    await act(async () => root.render(<ModalHarness />));
    const invoker = container.querySelector<HTMLButtonElement>("button")!;
    invoker.focus();
    await act(async () => invoker.click());
    await act(async () => undefined);

    const dialog = container.querySelector<HTMLElement>("[role='dialog']")!;
    const dialogButtons = Array.from(dialog.querySelectorAll<HTMLButtonElement>("button"));
    expect(invoker.hasAttribute("inert")).toBe(true);
    expect(dialog.contains(document.activeElement)).toBe(true);

    dialogButtons[dialogButtons.length - 1].focus();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true }));
    expect(document.activeElement).toBe(dialogButtons[0]);

    dialogButtons[1].focus();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowLeft", bubbles: true }));
    expect(dialog.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(invoker);

    await act(async () => document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    await act(async () => undefined);
    expect(container.querySelector("[role='dialog']")).toBeNull();
    expect(invoker.hasAttribute("inert")).toBe(false);
    expect(document.activeElement).toBe(invoker);
  });

  it("focuses the primary recovery action in an actionable error", async () => {
    await act(async () => root.render(<ErrorPanel message="Playback failed" onRetry={vi.fn()} onClose={vi.fn()} />));
    await act(async () => undefined);
    const error = container.querySelector<HTMLElement>("[role='alertdialog']")!;
    expect(error.getAttribute("aria-modal")).toBe("true");
    expect(document.activeElement).toBe(error.querySelector("[data-tv-error-primary='true']"));
  });
});
