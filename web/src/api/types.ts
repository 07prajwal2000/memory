/**
 * API types mirroring .prompts/_api.md exactly.
 */

export type Context = {
  slug: string;
  is_global: boolean;
  created_at: string;
  expires_at: string | null;
  memory_count: number;
};

export type MemorySummary = {
  id: string;
  context: string;
  name: string;
  has_name: boolean;
  version: number;
  updated_at: string;
  updated_by: string;
  tags: string[];
  size: number;
};

export type Memory = MemorySummary & {
  body: string;
};

export type HitSource = 'bm25' | 'vec' | 'rerank' | 'fuzzy' | 'name';

export type Hit = {
  doc_id: string;
  context: string;
  name: string;
  heading: string;
  snippet: string;
  chunk: number;
  score: number;
  sources: HitSource[];
  matched_terms: string[];
};

export type ApiErrorKind =
  | 'not_found'
  | 'conflict'
  | 'invalid'
  | 'exists'
  | 'reserved'
  | 'unavailable'
  | 'internal';

export type ApiErrorDetails = {
  current_version?: number;
  [key: string]: unknown;
};

export type ApiErrorResponse = {
  error: {
    kind: ApiErrorKind;
    message: string;
    details?: ApiErrorDetails;
  };
};

export type ServerModelStatus = {
  embed: {
    state: 'stopped' | 'starting' | 'ready' | 'error';
    detail?: string;
  };
  rerank: {
    enabled: boolean;
    state: 'stopped' | 'starting' | 'ready' | 'error' | 'disabled';
    detail?: string;
  };
};

export type ServerStatus = {
  version: string;
  data_dir: string;
  uptime_s: number;
  config: {
    default_temp_expiry_days: number;
    rerank: boolean;
  };
  models: ServerModelStatus;
  index: {
    docs: number;
    chunks: number;
    in_sync: boolean;
    pending: number;
  };
};

export type CreateContextPayload = {
  slug?: string;
  temp?: boolean;
  expiry_days?: number | null;
};

export type UpdateContextPayload = {
  expiry_days: number | null;
};

export type ListMemoriesParams = {
  q?: string;
  limit?: number;
  offset?: number;
  sort?: 'name' | 'updated';
};

export type ListMemoriesResponse = {
  items: MemorySummary[];
  total: number;
};

export type GetMemoryParams = {
  context?: string;
} & (
  | { id: string; name?: never }
  | { name: string; id?: never }
);

export type UpsertMemoryPayload = {
  context?: string;
  id?: string;
  name?: string;
  body: string;
  tags?: string[];
  append?: boolean;
  if_version?: number;
  updated_by?: string;
};

export type UpsertMemoryResponse = Memory & {
  index_warning?: string;
};

export type SearchParams = {
  q: string;
  context?: string;
  no_global?: boolean;
  fuzzy?: boolean;
  limit?: number;
  per_doc?: number;
};

export type SearchResponse = {
  hits: Hit[];
  degraded: string[];
  took_ms: number;
};

export type SyncResponse = {
  indexed: number;
  removed: number;
  errors: string[];
};
