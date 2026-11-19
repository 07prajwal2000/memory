import {
  Context,
  Hit,
  Memory,
  MemorySummary,
  ServerStatus,
  UpsertMemoryPayload,
} from '../api/types';
import {
  isReservedContext,
  normalizeMemoryName,
  validateBodySize,
  validateMemoryName,
  validateSlug,
} from '../lib/validation';

export interface MockDbState {
  contexts: Map<string, Context>;
  memories: Map<string, Memory>;
  status: ServerStatus;
  simulateIndexWarning: boolean;
  simulateDegradedSearch: boolean;
  simulateInternalError: boolean;
}

export class MockDatabase {
  private contexts = new Map<string, Context>();
  private memories = new Map<string, Memory>();
  private status!: ServerStatus;
  private simulateIndexWarning = false;
  private simulateDegradedSearch = false;
  private simulateInternalError = false;

  constructor() {
    this.seed();
  }

  public setIndexWarning(enabled: boolean) {
    this.simulateIndexWarning = enabled;
  }

  public setDegradedSearch(enabled: boolean) {
    this.simulateDegradedSearch = enabled;
  }

  public setInternalError(enabled: boolean) {
    this.simulateInternalError = enabled;
  }

  public reset() {
    this.contexts.clear();
    this.memories.clear();
    this.simulateIndexWarning = false;
    this.simulateDegradedSearch = false;
    this.simulateInternalError = false;
    this.seed();
  }

  private seed() {
    const now = new Date();
    const isoNow = now.toISOString();

    // 1. Contexts
    // Global context
    this.contexts.set('global', {
      slug: 'global',
      is_global: true,
      created_at: new Date(now.getTime() - 30 * 86400000).toISOString(),
      expires_at: null,
      memory_count: 0,
    });

    // Permanent context 1: core-system
    this.contexts.set('core-system', {
      slug: 'core-system',
      is_global: false,
      created_at: new Date(now.getTime() - 20 * 86400000).toISOString(),
      expires_at: null,
      memory_count: 0,
    });

    // Permanent context 2: devops-infra
    this.contexts.set('devops-infra', {
      slug: 'devops-infra',
      is_global: false,
      created_at: new Date(now.getTime() - 14 * 86400000).toISOString(),
      expires_at: null,
      memory_count: 0,
    });

    // Temp context: tmp-debug-investigation (expires in ~18 hours from now)
    const tempExpiry = new Date(now.getTime() + 18 * 3600000).toISOString();
    this.contexts.set('tmp-debug-investigation', {
      slug: 'tmp-debug-investigation',
      is_global: false,
      created_at: new Date(now.getTime() - 1 * 86400000).toISOString(),
      expires_at: tempExpiry,
      memory_count: 0,
    });

    // 2. Status
    this.status = {
      version: 'v0.5.0-alpha',
      data_dir: '/var/lib/mem/data',
      uptime_s: 34210,
      config: {
        default_temp_expiry_days: 7,
        rerank: false,
      },
      models: {
        embed: {
          state: 'ready',
          detail: 'bge-small-en-v1.5 Q8_0 GGUF (384-d int8)',
        },
        rerank: {
          enabled: false,
          state: 'disabled',
          detail: 'jina-reranker-v1-tiny-en (off by default)',
        },
      },
      index: {
        docs: 0,
        chunks: 0,
        in_sync: true,
        pending: 0,
      },
    };

    // 3. Seed memories
    this.seedMemories(isoNow);
    this.refreshCounts();
  }

  private seedMemories(isoNow: string) {
    // Pinned default file: global/readme.md
    this.addRawMemory({
      id: 'mem_default_readme',
      context: 'global',
      name: 'readme.md',
      has_name: true,
      version: 3,
      updated_at: isoNow,
      updated_by: 'system',
      tags: ['system', 'instructions', 'agents'],
      body: `# System Instructions & Conventions (mem)

Welcome to **mem**, the long-term memory store for local AI agents.

## Core Rules for Agents
1. Always search memories before asking the user for established project context.
2. Store key architectural decisions and reusable learnings in the appropriate context.
3. Keep markdown notes atomic, well-titled, and tagged for fast retrieval.
4. Temporary contexts auto-expire after 7 days unless made permanent by a developer.
`,
    });

    // Additional global memories
    const globalItems = [
      {
        name: 'agents/prompting-guide.md',
        tags: ['agents', 'prompts'],
        body: '# Prompting Guide\n\nTips on structuring system prompts for multi-agent workflows.\n\n- Be concise\n- Quote file paths with file:// scheme\n- Keep context updated',
      },
      {
        name: 'agents/error-conventions.md',
        tags: ['agents', 'conventions'],
        body: '# Error Conventions\n\nStandard error formats across all agent services.\n\nAll errors return `{ error: { kind, message } }`.',
      },
      {
        name: 'glossary/terminology.md',
        tags: ['glossary'],
        body: '# Glossary\n\n- **Context**: A named namespace for memories.\n- **Memory**: A markdown document.\n- **Chunk**: A 300-token embedding piece.',
      },
      {
        name: 'guidelines/security.md',
        tags: ['security', 'compliance'],
        body: '# Security Guidelines\n\n- Never log secrets or API tokens.\n- Localhost binding only.\n- Validate all inputs strictly.',
      },
      {
        name: 'architecture/overview.md',
        tags: ['architecture'],
        body: '# Overall Architecture\n\nSingle binary containing CLI, MCP server over stdio, and embedded Web Dashboard.',
      },
    ];

    for (const item of globalItems) {
      this.addRawMemory({
        id: `mem_glob_${Math.random().toString(36).substring(2, 9)}`,
        context: 'global',
        name: item.name,
        has_name: true,
        version: 1,
        updated_at: isoNow,
        updated_by: 'alice',
        tags: item.tags,
        body: item.body,
      });
    }

    // core-system memories (25+ items with nested paths)
    const corePaths = [
      'storage/engine.md',
      'storage/sqlite-vec.md',
      'storage/fts5-indexing.md',
      'storage/wal-mode.md',
      'search/hybrid-fusion.md',
      'search/rrf-algorithm.md',
      'search/reranking.md',
      'search/benchmarks.md',
      'models/embeddings-bge.md',
      'models/quantization-int8.md',
      'models/llama-sidecar.md',
      'mcp/stdio-protocol.md',
      'mcp/tool-definitions.md',
      'mcp/instructions-handler.md',
      'api/rest-v1-contract.md',
      'api/error-codes.md',
      'api/bearer-auth.md',
      'cli/commands.md',
      'cli/interactive-init.md',
      'cli/config-format.md',
      'packaging/npm-wrappers.md',
      'packaging/cross-compile.md',
      'tests/unit-testing.md',
      'tests/integration-suite.md',
      'tests/mock-patterns.md',
    ];

    for (let i = 0; i < corePaths.length; i++) {
      const p = corePaths[i];
      const category = p.split('/')[0];
      this.addRawMemory({
        id: `mem_core_${i.toString().padStart(3, '0')}`,
        context: 'core-system',
        name: p,
        has_name: true,
        version: (i % 4) + 1,
        updated_at: new Date(Date.now() - (i + 1) * 3600000 * 4).toISOString(),
        updated_by: i % 2 === 0 ? 'bob' : 'carol',
        tags: [category, 'backend', i % 3 === 0 ? 'v1' : 'stable'],
        body: `# Documentation: ${p}\n\nTechnical specifications and design details for **${p}**.\n\n## Section 1\nDetailed notes on implementation, edge cases, and performance considerations.\n\n## Verification\nVerified with test suites and benchmarks.`,
      });
    }

    // devops-infra memories (20+ items)
    const infraPaths = [
      'docker/compose-local.md',
      'docker/multi-arch-build.md',
      'ci/github-actions.md',
      'ci/lint-pipeline.md',
      'ci/test-matrix.md',
      'monitoring/metrics.md',
      'monitoring/health-checks.md',
      'monitoring/alerts.md',
      'k8s/deployments.md',
      'k8s/secrets.md',
      'k8s/ingress.md',
      'backup/sqlite-snapshots.md',
      'backup/disaster-recovery.md',
      'security/tls-termination.md',
      'security/firewall-rules.md',
      'scripts/release-tagging.md',
      'scripts/smoke-tests.md',
      'scripts/bump-version.md',
      'runbooks/incident-response.md',
      'runbooks/db-corruption.md',
    ];

    for (let i = 0; i < infraPaths.length; i++) {
      const p = infraPaths[i];
      const category = p.split('/')[0];
      this.addRawMemory({
        id: `mem_infra_${i.toString().padStart(3, '0')}`,
        context: 'devops-infra',
        name: p,
        has_name: true,
        version: 1,
        updated_at: new Date(Date.now() - (i + 1) * 3600000 * 8).toISOString(),
        updated_by: 'devops-bot',
        tags: [category, 'infra', 'ops'],
        body: `# Runbook / Infra: ${p}\n\nInfrastructure notes and operational steps for ${p}.\n\n\`\`\`bash\n# Standard maintenance command\nmem sync\n\`\`\``,
      });
    }

    // tmp-debug-investigation memories (15+ items)
    const debugPaths = [
      'traces/request-404-issue.md',
      'traces/sqlite-vec-lock.md',
      'traces/goroutine-leak.md',
      'traces/memory-spike-profile.md',
      'dumps/stacktrace-crash.md',
      'dumps/heap-snapshot.md',
      'repro/curl-commands.md',
      'repro/test-payload.md',
      'fixes/patch-candidate-1.md',
      'fixes/patch-candidate-2.md',
      'notes/triage-alice.md',
      'notes/triage-bob.md',
      'notes/findings.md',
      'unnamed-1',
      'unnamed-2',
    ];

    for (let i = 0; i < debugPaths.length; i++) {
      const p = debugPaths[i];
      const isUnnamed = p.startsWith('unnamed');
      const id = `mem_dbg_${i.toString().padStart(3, '0')}`;
      this.addRawMemory({
        id,
        context: 'tmp-debug-investigation',
        name: isUnnamed ? `${id}.md` : p,
        has_name: !isUnnamed,
        version: 1,
        updated_at: new Date(Date.now() - i * 1800000).toISOString(),
        updated_by: 'investigator',
        tags: ['debug', 'temp', 'p1'],
        body: `# Debug Log: ${p}\n\nDetailed crash dump, reproduction logs, and investigation notes.\n\nTimestamp: ${isoNow}\nSeverity: High`,
      });
    }
  }

  private addRawMemory(mem: Omit<Memory, 'size'>) {
    const size = new TextEncoder().encode(mem.body).length;
    this.memories.set(mem.id, {
      ...mem,
      size,
    });
  }

  private refreshCounts() {
    const counts = new Map<string, number>();
    for (const mem of this.memories.values()) {
      counts.set(mem.context, (counts.get(mem.context) ?? 0) + 1);
    }
    for (const [slug, ctx] of this.contexts.entries()) {
      ctx.memory_count = counts.get(slug) ?? 0;
    }
    this.status.index.docs = this.memories.size;
    this.status.index.chunks = this.memories.size * 3;
  }

  // --- API Handlers ---

  public getStatus(): ServerStatus {
    this.checkInternalError();
    this.refreshCounts();
    return { ...this.status };
  }

  public listContexts(): Context[] {
    this.checkInternalError();
    this.refreshCounts();
    const result: Context[] = [];
    const now = new Date().getTime();

    // Remove or filter expired
    for (const [slug, ctx] of this.contexts.entries()) {
      if (ctx.expires_at && new Date(ctx.expires_at).getTime() <= now) {
        // Expired: delete memories and context
        this.deleteContextInternal(slug);
        continue;
      }
      result.push({ ...ctx });
    }

    // Sort: global first, then by slug
    result.sort((a, b) => {
      if (a.is_global) return -1;
      if (b.is_global) return 1;
      return a.slug.localeCompare(b.slug);
    });

    return result;
  }

  public getContext(slug: string): Context {
    this.checkInternalError();
    const ctx = this.contexts.get(slug);
    if (!ctx) {
      throw { kind: 'not_found', message: `Context "${slug}" not found`, status: 404 };
    }
    if (ctx.expires_at && new Date(ctx.expires_at).getTime() <= Date.now()) {
      this.deleteContextInternal(slug);
      throw { kind: 'not_found', message: `Context "${slug}" has expired`, status: 404 };
    }
    this.refreshCounts();
    return { ...ctx };
  }

  public createContext(payload: {
    slug?: string;
    temp?: boolean;
    expiry_days?: number | null;
  }): Context {
    this.checkInternalError();
    let slug = payload.slug?.trim().toLowerCase();
    const isAutoNamed = !slug;

    if (isAutoNamed) {
      slug = `tmp-${Math.random().toString(36).substring(2, 6)}`;
    } else {
      const val = validateSlug(slug!);
      if (!val.valid) {
        const isReserved = isReservedContext(slug!);
        throw {
          kind: isReserved ? 'reserved' : 'invalid',
          message: val.error || 'Invalid slug',
          status: isReserved ? 403 : 400,
        };
      }
    }

    if (this.contexts.has(slug!)) {
      throw { kind: 'exists', message: `Context "${slug}" already exists`, status: 409 };
    }

    let expiresAt: string | null = null;
    const isTemp = payload.temp ?? isAutoNamed;

    if (isTemp) {
      const days =
        payload.expiry_days !== undefined && payload.expiry_days !== null
          ? payload.expiry_days
          : this.status.config.default_temp_expiry_days;
      if (days <= 0) {
        throw { kind: 'invalid', message: 'expiry_days must be positive', status: 400 };
      }
      expiresAt = new Date(Date.now() + days * 86400000).toISOString();
    }

    const newCtx: Context = {
      slug: slug!,
      is_global: false,
      created_at: new Date().toISOString(),
      expires_at: expiresAt,
      memory_count: 0,
    };

    this.contexts.set(slug!, newCtx);
    return { ...newCtx };
  }

  public updateContext(slug: string, payload: { expiry_days: number | null }): Context {
    this.checkInternalError();
    const ctx = this.getContext(slug);
    if (ctx.is_global) {
      throw { kind: 'reserved', message: 'Cannot modify global context expiry', status: 403 };
    }

    if (payload.expiry_days === null) {
      ctx.expires_at = null; // Permanent
    } else {
      if (payload.expiry_days <= 0) {
        throw { kind: 'invalid', message: 'expiry_days must be positive', status: 400 };
      }
      ctx.expires_at = new Date(Date.now() + payload.expiry_days * 86400000).toISOString();
    }

    return { ...ctx };
  }

  public deleteContext(slug: string): void {
    this.checkInternalError();
    const ctx = this.getContext(slug);
    if (ctx.is_global) {
      throw { kind: 'reserved', message: 'Cannot delete global context', status: 403 };
    }
    this.deleteContextInternal(slug);
  }

  private deleteContextInternal(slug: string) {
    this.contexts.delete(slug);
    for (const [id, mem] of this.memories.entries()) {
      if (mem.context === slug) {
        this.memories.delete(id);
      }
    }
    this.refreshCounts();
  }

  public listMemories(
    slug: string,
    params?: { q?: string; limit?: number; offset?: number; sort?: 'name' | 'updated' },
  ): { items: MemorySummary[]; total: number } {
    this.checkInternalError();
    this.getContext(slug); // Validate context exists and is not expired

    let list: Memory[] = [];
    for (const mem of this.memories.values()) {
      if (mem.context === slug) {
        list.push(mem);
      }
    }

    // Filter by name substring
    if (params?.q) {
      const q = params.q.toLowerCase();
      list = list.filter((m) => m.name.toLowerCase().includes(q));
    }

    // Sort
    const sort = params?.sort || 'name';
    list.sort((a, b) => {
      if (sort === 'updated') {
        return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime();
      }
      return a.name.localeCompare(b.name);
    });

    const total = list.length;
    const offset = params?.offset ?? 0;
    const limit = Math.min(params?.limit ?? 200, 1000);
    const paginated = list.slice(offset, offset + limit);

    const items: MemorySummary[] = paginated.map(
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      ({ body, ...summary }) => summary,
    );

    return { items, total };
  }

  public getMemory(params: { context?: string; id?: string; name?: string }): Memory {
    this.checkInternalError();
    const context = params.context || 'global';
    const { id, name } = params;

    if (!id && !name) {
      throw { kind: 'invalid', message: 'Either id or name must be specified', status: 400 };
    }
    if (id && name) {
      throw { kind: 'invalid', message: 'Cannot specify both id and name', status: 400 };
    }

    this.getContext(context);

    if (id) {
      const mem = this.memories.get(id);
      if (!mem || (params.context && mem.context !== context)) {
        throw { kind: 'not_found', message: `Memory id "${id}" not found`, status: 404 };
      }
      return { ...mem };
    }

    const normName = normalizeMemoryName(name!);
    for (const mem of this.memories.values()) {
      if (mem.context === context && mem.name.toLowerCase() === normName) {
        return { ...mem };
      }
    }

    throw {
      kind: 'not_found',
      message: `Memory "${normName}" not found in context "${context}"`,
      status: 404,
    };
  }

  public getDefaultMemory(): Memory {
    return this.getMemory({ context: 'global', name: 'readme.md' });
  }

  public upsertMemory(payload: UpsertMemoryPayload): Memory & { index_warning?: string } {
    this.checkInternalError();
    const context = payload.context || 'global';
    this.getContext(context);

    const sizeValidation = validateBodySize(payload.body);
    if (!sizeValidation.valid) {
      throw { kind: 'invalid', message: sizeValidation.error || 'Body too large', status: 400 };
    }

    let existing: Memory | undefined;

    if (payload.id) {
      existing = this.memories.get(payload.id);
      if (!existing) {
        throw {
          kind: 'not_found',
          message: `Memory with id "${payload.id}" not found`,
          status: 404,
        };
      }
    } else if (payload.name) {
      const norm = normalizeMemoryName(payload.name);
      for (const m of this.memories.values()) {
        if (m.context === context && m.name.toLowerCase() === norm) {
          existing = m;
          break;
        }
      }
    }

    if (existing) {
      // Check version conflict
      if (payload.if_version !== undefined && payload.if_version !== existing.version) {
        throw {
          kind: 'conflict',
          message: `Version conflict: memory is at version ${existing.version}, expected ${payload.if_version}`,
          status: 409,
          details: { current_version: existing.version },
        };
      }

      // Check name rename validation
      let newName = existing.name;
      let hasName = existing.has_name;
      if (payload.name) {
        const val = validateMemoryName(payload.name);
        if (!val.valid) {
          throw { kind: 'invalid', message: val.error || 'Invalid name', status: 400 };
        }
        newName = normalizeMemoryName(payload.name);
        hasName = true;
      }

      const newBody = payload.append ? existing.body + '\n' + payload.body : payload.body;

      const updated: Memory = {
        ...existing,
        name: newName,
        has_name: hasName,
        body: newBody,
        size: new TextEncoder().encode(newBody).length,
        version: existing.version + 1,
        updated_at: new Date().toISOString(),
        updated_by: payload.updated_by || 'user',
        tags: payload.tags || existing.tags,
      };

      this.memories.set(updated.id, updated);
      this.refreshCounts();

      const res: Memory & { index_warning?: string } = { ...updated };
      if (this.simulateIndexWarning) {
        res.index_warning = 'Memory saved to disk, but vector indexing failed. Run /sync to repair.';
      }
      return res;
    }

    // Create new
    const id = payload.id || `mem_${Math.random().toString(36).substring(2, 9)}`;
    let name = '';
    let hasName = false;

    if (payload.name) {
      const val = validateMemoryName(payload.name);
      if (!val.valid) {
        throw { kind: 'invalid', message: val.error || 'Invalid name', status: 400 };
      }
      name = normalizeMemoryName(payload.name);
      hasName = true;
    } else {
      name = `${id}.md`;
      hasName = false;
    }

    const created: Memory = {
      id,
      context,
      name,
      has_name: hasName,
      version: 1,
      updated_at: new Date().toISOString(),
      updated_by: payload.updated_by || 'user',
      tags: payload.tags || [],
      body: payload.body,
      size: new TextEncoder().encode(payload.body).length,
    };

    this.memories.set(id, created);
    this.refreshCounts();

    const res: Memory & { index_warning?: string } = { ...created };
    if (this.simulateIndexWarning) {
      res.index_warning = 'Memory saved to disk, but vector indexing failed. Run /sync to repair.';
    }
    return res;
  }

  public deleteMemory(id: string): void {
    this.checkInternalError();
    const mem = this.memories.get(id);
    if (!mem) {
      throw { kind: 'not_found', message: `Memory "${id}" not found`, status: 404 };
    }

    if (mem.context === 'global' && mem.name === 'readme.md') {
      throw { kind: 'reserved', message: 'Default file "global/readme.md" cannot be deleted', status: 403 };
    }

    this.memories.delete(id);
    this.refreshCounts();
  }

  public search(params: {
    q: string;
    context?: string;
    no_global?: boolean;
    limit?: number;
    per_doc?: number;
  }): { hits: Hit[]; degraded: string[]; took_ms: number } {
    this.checkInternalError();
    const q = params.q?.trim();
    if (!q) {
      throw { kind: 'invalid', message: 'Search query q cannot be empty', status: 400 };
    }

    if (params.no_global && !params.context) {
      throw {
        kind: 'invalid',
        message: 'no_global cannot be used without specifying a context',
        status: 400,
      };
    }

    const allowedContexts = new Set<string>();
    if (!params.no_global) {
      allowedContexts.add('global');
    }
    if (params.context) {
      this.getContext(params.context); // Validate
      allowedContexts.add(params.context);
    }

    const hits: Hit[] = [];
    const queryLower = q.toLowerCase();

    for (const mem of this.memories.values()) {
      if (!allowedContexts.has(mem.context)) continue;

      const bodyLower = mem.body.toLowerCase();
      const nameLower = mem.name.toLowerCase();

      let matched = false;
      let score = 0.5;
      const sources: Hit['sources'] = ['bm25'];

      if (nameLower.includes(queryLower)) {
        matched = true;
        score += 0.35;
        sources.push('vec');
      }

      if (bodyLower.includes(queryLower)) {
        matched = true;
        score += 0.25;
      }

      for (const t of mem.tags) {
        if (t.toLowerCase().includes(queryLower)) {
          matched = true;
          score += 0.2;
        }
      }

      if (matched) {
        // Extract a heading or preview snippet
        const lines = mem.body.split('\n');
        let heading = 'Overview';
        for (const line of lines) {
          if (line.startsWith('#')) {
            heading = line.replace(/^#+\s*/, '').trim();
            break;
          }
        }

        // Find match position for snippet
        const matchIdx = bodyLower.indexOf(queryLower);
        let snippet = '';
        if (matchIdx !== -1) {
          const start = Math.max(0, matchIdx - 40);
          const end = Math.min(mem.body.length, matchIdx + queryLower.length + 60);
          snippet = (start > 0 ? '...' : '') + mem.body.substring(start, end).trim() + '...';
        } else {
          snippet = mem.body.substring(0, 100).trim() + '...';
        }

        hits.push({
          doc_id: mem.id,
          context: mem.context,
          name: mem.name,
          heading,
          snippet,
          chunk: 1,
          score: Math.min(parseFloat(score.toFixed(3)), 0.99),
          sources,
        });
      }
    }

    hits.sort((a, b) => b.score - a.score);
    const limit = params.limit ?? 8;
    const finalHits = hits.slice(0, limit);

    const degraded: string[] = [];
    if (this.simulateDegradedSearch) {
      degraded.push('Embedder temporarily unavailable: returning BM25 keyword-only results.');
    }

    return {
      hits: finalHits,
      degraded,
      took_ms: Math.floor(Math.random() * 25) + 12,
    };
  }

  public sync(): { indexed: number; removed: number; errors: string[] } {
    this.checkInternalError();
    this.refreshCounts();
    return {
      indexed: this.memories.size,
      removed: 0,
      errors: [],
    };
  }

  private checkInternalError() {
    if (this.simulateInternalError) {
      throw { kind: 'internal', message: 'Simulated 500 internal server error', status: 500 };
    }
  }
}

export const mockDb = new MockDatabase();
