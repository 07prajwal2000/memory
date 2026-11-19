import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from './client';
import { queryKeys } from './keys';
import { Context, CreateContextPayload, UpdateContextPayload } from './types';

export async function fetchContexts(): Promise<Context[]> {
  return apiClient<Context[]>('/api/contexts');
}

export async function fetchContext(slug: string): Promise<Context> {
  return apiClient<Context>(`/api/contexts/${encodeURIComponent(slug)}`);
}

export async function createContext(data: CreateContextPayload): Promise<Context> {
  return apiClient<Context>('/api/contexts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
}

export async function updateContext(slug: string, data: UpdateContextPayload): Promise<Context> {
  return apiClient<Context>(`/api/contexts/${encodeURIComponent(slug)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
}

export async function deleteContext(slug: string): Promise<void> {
  return apiClient<void>(`/api/contexts/${encodeURIComponent(slug)}`, {
    method: 'DELETE',
  });
}

export function useContexts() {
  return useQuery({
    queryKey: queryKeys.contexts.all,
    queryFn: fetchContexts,
  });
}

export function useContextDetail(slug: string) {
  return useQuery({
    queryKey: queryKeys.contexts.detail(slug),
    queryFn: () => fetchContext(slug),
    enabled: Boolean(slug),
  });
}

export function useCreateContext() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: createContext,
    onSuccess: (newCtx) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.contexts.all });
      queryClient.setQueryData(queryKeys.contexts.detail(newCtx.slug), newCtx);
    },
  });
}

export function useUpdateContext() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ slug, data }: { slug: string; data: UpdateContextPayload }) =>
      updateContext(slug, data),
    onSuccess: (updatedCtx) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.contexts.all });
      queryClient.setQueryData(queryKeys.contexts.detail(updatedCtx.slug), updatedCtx);
    },
  });
}

export function useDeleteContext() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (slug: string) => deleteContext(slug),
    onSuccess: (_, deletedSlug) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.contexts.all });
      queryClient.removeQueries({ queryKey: queryKeys.contexts.detail(deletedSlug) });
      queryClient.removeQueries({ queryKey: ['memories', 'list', deletedSlug] });
    },
  });
}
