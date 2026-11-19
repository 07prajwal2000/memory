import { createContext, useContext } from 'react';

export function safeGetLocalStorage(key: string, fallback: string | null = null): string | null {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

export function safeSetLocalStorage(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    // safe ignore for quota or security exceptions
  }
}

export function safeGetSessionStorage(key: string, fallback: string | null = null): string | null {
  try {
    return sessionStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

export function safeSetSessionStorage(key: string, value: string): void {
  try {
    sessionStorage.setItem(key, value);
  } catch {
    // safe ignore
  }
}

export interface MemoryWorkspaceContextValue {
  isDirty: boolean;
  setIsDirty: (dirty: boolean) => void;
  requestNavigate: (to: string) => void;
  isFilesDrawerOpen: boolean;
  openFilesDrawer: () => void;
  closeFilesDrawer: () => void;
  isCollapsed: boolean;
  toggleCollapse: () => void;
  panelWidth: number;
  setPanelWidth: (width: number) => void;
}

const defaultContextValue: MemoryWorkspaceContextValue = {
  isDirty: false,
  setIsDirty: () => {},
  requestNavigate: (to: string) => {
    window.location.hash = to;
  },
  isFilesDrawerOpen: false,
  openFilesDrawer: () => {},
  closeFilesDrawer: () => {},
  isCollapsed: false,
  toggleCollapse: () => {},
  panelWidth: 320,
  setPanelWidth: () => {},
};

export const MemoryWorkspaceContext = createContext<MemoryWorkspaceContextValue>(defaultContextValue);

export function useMemoryWorkspace(): MemoryWorkspaceContextValue {
  return useContext(MemoryWorkspaceContext);
}
