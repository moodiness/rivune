type StorageName = "localStorage" | "sessionStorage";
type SafeStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

function createSafeStorage(name: StorageName): SafeStorage {
  const memory = new Map<string, string | null>();
  const dirty = new Set<string>();
  const backingStore = (): Storage | null => {
    try {
      return window[name];
    } catch {
      return null;
    }
  };

  return {
    getItem(key) {
      if (dirty.has(key)) return memory.get(key) ?? null;
      const store = backingStore();
      if (store !== null) {
        try {
          const value = store.getItem(key);
          memory.set(key, value);
          return value;
        } catch {
          // Fall through to the last value observed in this tab.
        }
      }
      return memory.get(key) ?? null;
    },
    setItem(key, value) {
      memory.set(key, value);
      const store = backingStore();
      if (store === null) {
        dirty.add(key);
        return;
      }
      try {
        store.setItem(key, value);
        dirty.delete(key);
      } catch {
        dirty.add(key);
      }
    },
    removeItem(key) {
      memory.set(key, null);
      const store = backingStore();
      if (store === null) {
        dirty.add(key);
        return;
      }
      try {
        store.removeItem(key);
        dirty.delete(key);
      } catch {
        dirty.add(key);
      }
    },
  };
}

export const safeLocalStorage = createSafeStorage("localStorage");
export const safeSessionStorage = createSafeStorage("sessionStorage");
