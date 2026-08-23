import "core-js/es/promise/with-resolvers";

type PromiseResolvers<T> = {
  promise: Promise<T>;
  resolve(value: T | PromiseLike<T>): void;
  reject(reason?: unknown): void;
};

function withResolvers<T>(): PromiseResolvers<T> {
  return (Promise as PromiseConstructor & { withResolvers<U>(): PromiseResolvers<U> }).withResolvers<T>();
}

const databaseName = "rivune-tv-runtime";
const storeName = "state";
const stateKey = "runtime";

export interface RuntimeStore {
  load(): Promise<unknown>;
  save(value: unknown): Promise<void>;
}

function transactionCompletion(transaction: IDBTransaction): Promise<void> {
  const { promise, resolve, reject } = withResolvers<void>();
  transaction.oncomplete = () => resolve();
  transaction.onerror = () => reject(transaction.error ?? new Error("The TV runtime storage transaction failed."));
  transaction.onabort = () => reject(transaction.error ?? new Error("The TV runtime storage transaction was aborted."));
  return promise;
}

export async function openRuntimeStore(): Promise<RuntimeStore> {
  if (!window.indexedDB) throw new Error("IndexedDB is unavailable.");
  const opening = withResolvers<IDBDatabase>();
  const request = window.indexedDB.open(databaseName, 1);
  request.onupgradeneeded = () => {
    if (!request.result.objectStoreNames.contains(storeName)) request.result.createObjectStore(storeName);
  };
  request.onsuccess = () => opening.resolve(request.result);
  request.onerror = () => opening.reject(request.error ?? new Error("The TV runtime storage could not be opened."));
  request.onblocked = () => opening.reject(new Error("The TV runtime storage upgrade is blocked."));
  const database = await opening.promise;
  database.onversionchange = () => database.close();
  return {
    async load(): Promise<unknown> {
      const transaction = database.transaction(storeName, "readonly");
      const request = transaction.objectStore(storeName).get(stateKey);
      const reading = withResolvers<unknown>();
      request.onsuccess = () => reading.resolve(request.result);
      request.onerror = () => reading.reject(request.error ?? new Error("The TV runtime state could not be read."));
      const result = await reading.promise;
      await transactionCompletion(transaction);
      return result;
    },
    async save(value: unknown): Promise<void> {
      const transaction = database.transaction(storeName, "readwrite");
      transaction.objectStore(storeName).put(value, stateKey);
      await transactionCompletion(transaction);
    },
  };
}
