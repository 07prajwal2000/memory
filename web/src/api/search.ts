import { useQuery } from '@tanstack/react-query';
import { apiClient } from './client';
import { queryKeys } from './keys';
import { SearchParams, SearchResponse } from './types';

export async function searchMemories(
  params: SearchParams,
  signal?: AbortSignal,
): Promise<SearchResponse> {
  return apiClient<SearchResponse>('/api/search', {
    signal,
    params: {
      q: params.q,
      context: params.context,
      no_global: params.no_global ? 'true' : undefined,
      fuzzy: params.fuzzy !== undefined ? (params.fuzzy ? '1' : '0') : undefined,
      limit: params.limit,
      per_doc: params.per_doc,
    },
  });
}

export function useSearch(params: SearchParams, enabled = true) {
  return useQuery({
    queryKey: queryKeys.search(params),
    queryFn: ({ signal }) => searchMemories(params, signal),
    enabled: enabled && Boolean(params.q.trim()),
    staleTime: 5000,
  });
}
