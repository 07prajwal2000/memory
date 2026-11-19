import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from './client';
import { queryKeys } from './keys';
import {
  GetMemoryParams,
  ListMemoriesParams,
  ListMemoriesResponse,
  Memory,
  UpsertMemoryPayload,
  UpsertMemoryResponse,
} from './types';

export async function fetchMemories(
  slug: string,
  params?: ListMemoriesParams,
): Promise<ListMemoriesResponse> {
  return apiClient<ListMemoriesResponse>(`/api/contexts/${encodeURIComponent(slug)}/memories`, {
    params: {
      q: params?.q,
      limit: params?.limit,
      offset: params?.offset,
      sort: params?.sort,
    },
  });
}

export async function fetchMemory(params: GetMemoryParams): Promise<Memory> {
  const queryParams: Record<string, string> = {};
  if (params.context) {
    queryParams.context = params.context;
  }
  if (params.id) {
    queryParams.id = params.id;
  } else if (params.name) {
    queryParams.name = params.name;
  }
  return apiClient<Memory>('/api/memory', { params: queryParams });
}

export async function fetchDefaultMemory(): Promise<Memory> {
  return apiClient<Memory>('/api/default');
}

export async function upsertMemory(data: UpsertMemoryPayload): Promise<UpsertMemoryResponse> {
  return apiClient<UpsertMemoryResponse>('/api/memory', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
}

export async function deleteMemory(id: string): Promise<void> {
  return apiClient<void>('/api/memory', {
    method: 'DELETE',
    params: { id },
  });
}

export function useMemoriesList(slug: string, params?: ListMemoriesParams) {
  return useQuery({
    queryKey: queryKeys.memories.list(slug, params),
    queryFn: () => fetchMemories(slug, params),
    enabled: Boolean(slug),
  });
}

export function useMemoryDetail(context: string, idOrName?: string, isName = false) {
  return useQuery({
    queryKey: queryKeys.memories.detail(context, idOrName ?? ''),
    queryFn: () => {
      if (!idOrName) throw new Error('id or name required');
      if (isName) {
        return fetchMemory({ context, name: idOrName });
      }
      return fetchMemory({ context, id: idOrName });
    },
    enabled: Boolean(context && idOrName),
  });
}

export function useDefaultMemory() {
  return useQuery({
    queryKey: queryKeys.memories.default,
    queryFn: fetchDefaultMemory,
  });
}

export function useUpsertMemory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: upsertMemory,
    onSuccess: (savedMem) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.contexts.all });
      queryClient.invalidateQueries({
        queryKey: ['memories', 'list', savedMem.context],
      });
      queryClient.setQueryData(
        queryKeys.memories.detail(savedMem.context, savedMem.id),
        savedMem,
      );
      if (savedMem.name) {
        queryClient.setQueryData(
          queryKeys.memories.detail(savedMem.context, savedMem.name),
          savedMem,
        );
      }
      if (savedMem.context === 'global' && savedMem.name === 'readme.md') {
        queryClient.setQueryData(queryKeys.memories.default, savedMem);
      }
    },
  });
}

export function useDeleteMemory() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, context }: { id: string; context: string }) =>
      deleteMemory(id).then(() => ({ id, context })),
    onSuccess: ({ id, context }) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.contexts.all });
      queryClient.invalidateQueries({
        queryKey: ['memories', 'list', context],
      });
      queryClient.removeQueries({
        queryKey: queryKeys.memories.detail(context, id),
      });
    },
  });
}
