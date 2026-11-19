import { setupWorker } from 'msw/browser';
import { handlers, setMockLatency } from './handlers';

export const worker = setupWorker(...handlers);

export async function enableMocking() {
  setMockLatency(true);
  return worker.start({
    serviceWorker: {
      url: '/mockServiceWorker.js',
    },
  });
}
