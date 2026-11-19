import { ListMemoriesParams, SearchParams } from './types';

export const queryKeys = {
  status: ['status'] as const,
  contexts: {
    all: ['contexts'] as const,
    detail: (slug: string) => ['contexts', 'detail', slug] as const,
  },
  memories: {
    all: ['memories'] as const,
    list: (slug: string, params?: ListMemoriesParams) =>
      ['memories', 'list', slug, params ?? {}] as const,
    detail: (context: string, idOrName: string) =>
      ['memories', 'detail', context, idOrName] as const,
    default: ['memories', 'default'] as const,
  },
  search: (params: SearchParams) => ['search', params] as const,
};
