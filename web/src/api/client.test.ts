import { describe, it, expect, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { server } from '../mocks/node';
import {
  apiClient,
  ApiError,
  setStoredToken,
  getStoredToken,
  setUnauthorizedListener,
} from './client';
import { ApiErrorKind } from './types';

describe('apiClient', () => {
  const errorKinds: { kind: ApiErrorKind; status: number }[] = [
    { kind: 'not_found', status: 404 },
    { kind: 'conflict', status: 409 },
    { kind: 'invalid', status: 400 },
    { kind: 'exists', status: 409 },
    { kind: 'reserved', status: 403 },
    { kind: 'unavailable', status: 503 },
    { kind: 'internal', status: 500 },
  ];

  for (const { kind, status } of errorKinds) {
    it(`maps HTTP ${status} to ApiError with kind "${kind}"`, async () => {
      server.use(
        http.get('/api/test-error', () => {
          return HttpResponse.json(
            {
              error: {
                kind,
                message: `Failed with kind ${kind}`,
                details: { info: 'test-detail' },
              },
            },
            { status },
          );
        }),
      );

      try {
        await apiClient('/api/test-error');
        expect.unreachable('Should have thrown an ApiError');
      } catch (err: unknown) {
        expect(err).toBeInstanceOf(ApiError);
        const apiErr = err as ApiError;
        expect(apiErr.kind).toBe(kind);
        expect(apiErr.status).toBe(status);
        expect(apiErr.message).toBe(`Failed with kind ${kind}`);
        expect(apiErr.details).toEqual({ info: 'test-detail' });
      }
    });
  }

  it('includes bearer token in Authorization header when token is stored', async () => {
    let capturedHeader: string | null = null;
    server.use(
      http.get('/api/test-auth-header', ({ request }) => {
        capturedHeader = request.headers.get('Authorization');
        return HttpResponse.json({ ok: true });
      }),
    );

    setStoredToken('test-secret-token-123');
    expect(getStoredToken()).toBe('test-secret-token-123');

    await apiClient('/api/test-auth-header');
    expect(capturedHeader).toBe('Bearer test-secret-token-123');

    setStoredToken(null);
    expect(getStoredToken()).toBeNull();
  });

  it('triggers unauthorizedListener and throws ApiError on 401', async () => {
    const unauthorizedSpy = vi.fn();
    setUnauthorizedListener(unauthorizedSpy);

    server.use(
      http.get('/api/test-401', () => {
        return new HttpResponse(null, { status: 401 });
      }),
    );

    try {
      await apiClient('/api/test-401');
      expect.unreachable('Should have thrown on 401');
    } catch (err: unknown) {
      expect(err).toBeInstanceOf(ApiError);
      const apiErr = err as ApiError;
      expect(apiErr.status).toBe(401);
      expect(unauthorizedSpy).toHaveBeenCalledTimes(1);
    } finally {
      setUnauthorizedListener(null);
    }
  });

  it('handles query parameters correctly', async () => {
    let capturedUrl = '';
    server.use(
      http.get('/api/test-params', ({ request }) => {
        capturedUrl = request.url;
        return HttpResponse.json({ ok: true });
      }),
    );

    await apiClient('/api/test-params', {
      params: { q: 'search-term', limit: 10, empty: undefined },
    });

    const parsed = new URL(capturedUrl);
    expect(parsed.searchParams.get('q')).toBe('search-term');
    expect(parsed.searchParams.get('limit')).toBe('10');
    expect(parsed.searchParams.has('empty')).toBe(false);
  });
});
