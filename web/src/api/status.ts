import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { apiClient } from './client';
import { queryKeys } from './keys';
import { ServerStatus, SyncResponse } from './types';

export async function fetchStatus(): Promise<ServerStatus> {
  return apiClient<ServerStatus>('/api/status');
}

export async function syncIndex(): Promise<SyncResponse> {
  return apiClient<SyncResponse>('/api/sync', {
    method: 'POST',
  });
}

export function useServerStatus(options?: { refetchInterval?: number | false }) {
  return useQuery({
    queryKey: queryKeys.status,
    queryFn: fetchStatus,
    refetchInterval: options?.refetchInterval ?? 15000,
    refetchIntervalInBackground: false,
  });
}

export function useSyncIndex() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: syncIndex,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.status });
      queryClient.invalidateQueries({ queryKey: queryKeys.contexts.all });
    },
  });
}
