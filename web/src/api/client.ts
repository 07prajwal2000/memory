import { ApiErrorKind, ApiErrorDetails, ApiErrorResponse } from './types';

export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly status: number;
  readonly details?: ApiErrorDetails;

  constructor(kind: ApiErrorKind, message: string, status: number, details?: ApiErrorDetails) {
    super(message);
    this.name = 'ApiError';
    this.kind = kind;
    this.status = status;
    this.details = details;
  }
}

const TOKEN_STORAGE_KEY = 'mem_auth_token';

let unauthorizedListener: (() => void) | null = null;

export function setUnauthorizedListener(listener: (() => void) | null) {
  unauthorizedListener = listener;
}

export function getStoredToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_STORAGE_KEY);
  } catch {
    return null;
  }
}

export function setStoredToken(token: string | null) {
  try {
    if (token) {
      sessionStorage.setItem(TOKEN_STORAGE_KEY, token);
    } else {
      sessionStorage.removeItem(TOKEN_STORAGE_KEY);
    }
  } catch {
    // Ignore storage errors in restricted contexts
  }
}

export function initTokenFromUrl(): string | null {
  if (typeof window === 'undefined') return null;
  try {
    const url = new URL(window.location.href);
    const token = url.searchParams.get('token');
    if (token) {
      setStoredToken(token);
      url.searchParams.delete('token');
      window.history.replaceState({}, document.title, url.toString());
      return token;
    }
  } catch {
    // URL parsing failed or unsupported
  }
  return getStoredToken();
}

export interface RequestOptions extends RequestInit {
  params?: Record<string, string | number | boolean | undefined | null>;
}

export async function apiClient<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { params, headers: customHeaders, ...rest } = options;

  let url = path;
  if (params) {
    const searchParams = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined && value !== null) {
        searchParams.append(key, String(value));
      }
    }
    const query = searchParams.toString();
    if (query) {
      url += (url.includes('?') ? '&' : '?') + query;
    }
  }

  const headers = new Headers(customHeaders);
  if (!headers.has('Accept')) {
    headers.set('Accept', 'application/json');
  }

  const token = getStoredToken();
  if (token) {
    headers.set('Authorization', `Bearer ${token}`);
  }

  const response = await fetch(url, {
    ...rest,
    headers,
  });

  if (response.status === 401) {
    if (unauthorizedListener) {
      unauthorizedListener();
    }
    throw new ApiError('reserved', 'Unauthorized: valid token required', 401);
  }

  if (response.status === 204) {
    return undefined as unknown as T;
  }

  const contentType = response.headers.get('content-type') || '';
  const isJson = contentType.includes('application/json');

  if (!response.ok) {
    let errorKind: ApiErrorKind = 'internal';
    let errorMessage = `HTTP Error ${response.status}: ${response.statusText}`;
    let details: ApiErrorDetails | undefined;

    if (isJson) {
      try {
        const json = (await response.json()) as ApiErrorResponse;
        if (json && json.error) {
          errorKind = json.error.kind || 'internal';
          errorMessage = json.error.message || errorMessage;
          details = json.error.details;
        }
      } catch {
        // Fallback to generic message
      }
    }

    // Default kind mapping if kind wasn't parsed
    if (errorKind === 'internal') {
      if (response.status === 404) errorKind = 'not_found';
      else if (response.status === 409) errorKind = 'conflict';
      else if (response.status === 400) errorKind = 'invalid';
      else if (response.status === 403) errorKind = 'reserved';
      else if (response.status === 503) errorKind = 'unavailable';
    }

    throw new ApiError(errorKind, errorMessage, response.status, details);
  }

  if (isJson) {
    return (await response.json()) as T;
  }

  return (await response.text()) as unknown as T;
}
