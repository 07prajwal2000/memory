import '@testing-library/jest-dom';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from '../mocks/node';
import { mockDb } from '../mocks/db';
import { setMockLatency, setRequireBearerToken } from '../mocks/handlers';

beforeAll(() => {
  setMockLatency(false);
  setRequireBearerToken(false);
  server.listen();
});

afterEach(() => {
  server.resetHandlers();
  mockDb.reset();
  setMockLatency(false);
  setRequireBearerToken(false);
  sessionStorage.clear();
  localStorage.clear();
});

afterAll(() => {
  server.close();
});

// Mock matchMedia for Mantine and color scheme
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});

// Mock ResizeObserver
globalThis.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
};

// Polyfill Range getClientRects / getBoundingClientRect for CodeMirror in jsdom
if (typeof Range !== 'undefined') {
  Range.prototype.getClientRects = function () {
    return [] as unknown as DOMRectList;
  };
  Range.prototype.getBoundingClientRect = function () {
    return {
      x: 0,
      y: 0,
      bottom: 0,
      height: 0,
      left: 0,
      right: 0,
      top: 0,
      width: 0,
      toJSON: () => {},
    };
  };
}

// Mock scrollTo
window.scrollTo = () => {};
