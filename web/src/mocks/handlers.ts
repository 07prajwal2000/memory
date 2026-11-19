import { http, HttpResponse, delay } from 'msw';
import { mockDb } from './db';
import {
  CreateContextPayload,
  ListMemoriesParams,
  SearchParams,
  UpdateContextPayload,
  UpsertMemoryPayload,
} from '../api/types';

// Artificial latency setting (default: true for browser, false for tests)
let enableArtificialLatency = false;
let requireBearerToken = false;
let expectedBearerToken = 'secret-test-token';

export function setMockLatency(enabled: boolean) {
  enableArtificialLatency = enabled;
}

export function setRequireBearerToken(required: boolean, token = 'secret-test-token') {
  requireBearerToken = required;
  expectedBearerToken = token;
}

async function maybeDelay() {
  if (enableArtificialLatency) {
    const ms = Math.floor(Math.random() * 200) + 100;
    await delay(ms);
  }
}

function checkAuth(request: Request): Response | null {
  if (!requireBearerToken) return null;
  const authHeader = request.headers.get('Authorization');
  if (!authHeader || authHeader !== `Bearer ${expectedBearerToken}`) {
    return HttpResponse.json(
      {
        error: {
          kind: 'reserved',
          message: 'Valid bearer token required',
        },
      },
      { status: 401 },
    );
  }
  return null;
}

function handleError(err: unknown) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const e = err as any;
  const status = e.status || 500;
  const kind = e.kind || 'internal';
  const message = e.message || 'Internal server error';
  const details = e.details;

  return HttpResponse.json(
    {
      error: {
        kind,
        message,
        details,
      },
    },
    { status },
  );
}

export const handlers = [
  // 1. GET /api/status
  http.get('/api/status', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const status = mockDb.getStatus();
      return HttpResponse.json(status);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 2. GET /api/contexts
  http.get('/api/contexts', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const list = mockDb.listContexts();
      return HttpResponse.json(list);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 3. POST /api/contexts
  http.post('/api/contexts', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const body = (await request.json()) as CreateContextPayload;
      const created = mockDb.createContext(body || {});
      return HttpResponse.json(created, { status: 201 });
    } catch (err) {
      return handleError(err);
    }
  }),

  // 4. GET /api/contexts/:slug
  http.get('/api/contexts/:slug', async ({ params, request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const slug = String(params.slug);
      const ctx = mockDb.getContext(slug);
      return HttpResponse.json(ctx);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 5. PATCH /api/contexts/:slug
  http.patch('/api/contexts/:slug', async ({ params, request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const slug = String(params.slug);
      const body = (await request.json()) as UpdateContextPayload;
      const updated = mockDb.updateContext(slug, body);
      return HttpResponse.json(updated);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 6. DELETE /api/contexts/:slug
  http.delete('/api/contexts/:slug', async ({ params, request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const slug = String(params.slug);
      mockDb.deleteContext(slug);
      return new HttpResponse(null, { status: 204 });
    } catch (err) {
      return handleError(err);
    }
  }),

  // 7. GET /api/contexts/:slug/memories
  http.get('/api/contexts/:slug/memories', async ({ params, request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const slug = String(params.slug);
      const url = new URL(request.url);
      const queryParams: ListMemoriesParams = {
        q: url.searchParams.get('q') || undefined,
        limit: url.searchParams.has('limit') ? Number(url.searchParams.get('limit')) : undefined,
        offset: url.searchParams.has('offset') ? Number(url.searchParams.get('offset')) : undefined,
        sort: (url.searchParams.get('sort') as 'name' | 'updated') || undefined,
      };

      const res = mockDb.listMemories(slug, queryParams);
      return HttpResponse.json(res);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 8. GET /api/memory
  http.get('/api/memory', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const url = new URL(request.url);
      const context = url.searchParams.get('context') || undefined;
      const id = url.searchParams.get('id') || undefined;
      const name = url.searchParams.get('name') || undefined;

      const mem = mockDb.getMemory({ context, id, name });
      return HttpResponse.json(mem);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 9. PUT /api/memory
  http.put('/api/memory', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const body = (await request.json()) as UpsertMemoryPayload;
      const res = mockDb.upsertMemory(body);
      // Status is 201 if created, 200 if updated. For simplicity, 200/201 both work
      const status = res.version === 1 ? 201 : 200;
      return HttpResponse.json(res, { status });
    } catch (err) {
      return handleError(err);
    }
  }),

  // 10. DELETE /api/memory
  http.delete('/api/memory', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const url = new URL(request.url);
      const id = url.searchParams.get('id');
      if (!id) {
        return HttpResponse.json(
          { error: { kind: 'invalid', message: 'Missing memory id' } },
          { status: 400 },
        );
      }
      mockDb.deleteMemory(id);
      return new HttpResponse(null, { status: 204 });
    } catch (err) {
      return handleError(err);
    }
  }),

  // 11. GET /api/default
  http.get('/api/default', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const def = mockDb.getDefaultMemory();
      return HttpResponse.json(def);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 12. GET /api/search
  http.get('/api/search', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const url = new URL(request.url);
      const q = url.searchParams.get('q') || '';
      const context = url.searchParams.get('context') || undefined;
      const no_global = url.searchParams.get('no_global') === 'true';
      const fuzzy = url.searchParams.has('fuzzy')
        ? url.searchParams.get('fuzzy') === '1' || url.searchParams.get('fuzzy') === 'true'
        : undefined;
      const limit = url.searchParams.has('limit') ? Number(url.searchParams.get('limit')) : undefined;
      const per_doc = url.searchParams.has('per_doc')
        ? Number(url.searchParams.get('per_doc'))
        : undefined;

      const searchParams: SearchParams = {
        q,
        context,
        no_global,
        fuzzy,
        limit,
        per_doc,
      };

      const res = mockDb.search(searchParams);
      return HttpResponse.json(res);
    } catch (err) {
      return handleError(err);
    }
  }),

  // 13. POST /api/sync
  http.post('/api/sync', async ({ request }) => {
    const authErr = checkAuth(request);
    if (authErr) return authErr;
    await maybeDelay();
    try {
      const res = mockDb.sync();
      return HttpResponse.json(res);
    } catch (err) {
      return handleError(err);
    }
  }),
];
