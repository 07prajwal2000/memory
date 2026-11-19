package cli

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"memory/internal/config"
	"memory/internal/engine"
	"memory/internal/setup"
	"memory/internal/store"
)

func lsCommand() command {
	return command{
		name:  "ls",
		usage: "mem ls [--context C] [--limit N]",
		parse: parseLS,
		run:   runLS,
	}
}

type lsFlags struct {
	context string
	limit   int
	pos     []string
}

func parseLS(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	f := &lsFlags{}
	fs.StringVar(&f.context, "context", "", "context slug (default global)")
	fs.IntVar(&f.limit, "limit", 0, "show at most N memories")
	pos, err := collectArgs(fs, args)
	if err != nil {
		return nil, err
	}
	f.pos = pos
	a.state = f
	return nil, nil
}

func runLS(a *app, _ []string) (int, error) {
	f := a.state.(*lsFlags)
	if len(f.pos) > 0 {
		return ExitUsage, usagef("mem ls takes no arguments; use --context to pick a context")
	}
	s, err := a.openStore()
	if err != nil {
		return ExitError, err
	}
	defer s.Close()
	if _, err := s.SweepExpired(); err != nil {
		return ExitError, err
	}
	slug := f.context
	if slug == "" {
		slug = store.GlobalContext
	}
	list, err := s.List(slug)
	if err != nil {
		return ExitError, err
	}
	if f.limit > 0 && len(list) > f.limit {
		list = list[:f.limit]
	}
	if a.globals.json {
		rows := make([]map[string]any, 0, len(list))
		for _, m := range list {
			rows = append(rows, map[string]any{
				"id":         m.ID,
				"context":    m.Context,
				"name":       m.Name,
				"version":    m.Version,
				"updated_at": m.UpdatedAt.UTC(),
				"updated_by": m.UpdatedBy,
				"tags":       m.Tags,
			})
		}
		return ExitOK, a.emit(rows)
	}
	if len(list) == 0 {
		a.say("no memories in %s\n", slug)
		return ExitOK, nil
	}
	a.say("%-40s %-12s %7s  %s\n", "NAME", "ID", "VERSION", "UPDATED")
	for _, m := range list {
		a.say("%-40s %-12s %7d  %s\n", m.Name, m.ID, m.Version, formatTime(m.UpdatedAt))
	}
	return ExitOK, nil
}

func getCommand() command {
	return command{
		name:  "get",
		usage: "mem get [name-or-id] [--context C]",
		parse: parseGet,
		run:   runGet,
	}
}

type getFlags struct {
	context string
	pos     []string
}

func parseGet(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	f := &getFlags{}
	fs.StringVar(&f.context, "context", "", "context slug (default global)")
	pos, err := collectArgs(fs, args)
	if err != nil {
		return nil, err
	}
	f.pos = pos
	a.state = f
	return nil, nil
}

func runGet(a *app, _ []string) (int, error) {
	f := a.state.(*getFlags)
	if len(f.pos) > 1 {
		return ExitUsage, usagef("mem get takes at most one name or id")
	}
	s, err := a.openStore()
	if err != nil {
		return ExitError, err
	}
	defer s.Close()
	if _, err := s.SweepExpired(); err != nil {
		return ExitError, err
	}

	if len(f.pos) == 0 {
		return runGetDefault(a, s, f.context)
	}

	m, err := s.Get(f.context, f.pos[0])
	if err != nil {
		return ExitError, err
	}
	if err := a.emit(memoryJSON(m)); err != nil {
		return ExitError, err
	}
	a.say("---\n")
	a.say("# %s (%s, v%d, updated %s by %s)\n", m.Name, m.ID, m.Version, formatTime(m.UpdatedAt), m.UpdatedBy)
	if len(m.Tags) > 0 {
		a.say("# tags: %s\n", strings.Join(m.Tags, ", "))
	}
	a.say("---\n%s", m.Body)
	if m.Body != "" && !strings.HasSuffix(m.Body, "\n") {
		a.say("\n")
	}
	return ExitOK, nil
}

// runGetDefault mirrors the MCP no-argument get: the default file plus the
// context list.
func runGetDefault(a *app, s *store.Store, contextSlug string) (int, error) {
	def, err := s.Get(store.GlobalContext, store.DefaultFileName)
	if err != nil {
		return ExitError, err
	}
	contexts, err := s.ListContexts()
	if err != nil {
		return ExitError, err
	}
	current := contextSlug
	if current == "" {
		current = store.GlobalContext
	}
	var memories []store.Memory
	if cur, err := s.GetContext(current); err == nil {
		if list, err := s.List(current); err == nil {
			memories = list
		}
		_ = cur
	}
	if a.globals.json {
		return ExitOK, a.emit(map[string]any{
			"default_file": memoryJSON(def),
			"context":      current,
			"contexts":     contextsJSON(contexts),
			"memories":     memoriesJSON(memories),
		})
	}
	a.say("%s", def.Body)
	if def.Body != "" && !strings.HasSuffix(def.Body, "\n") {
		a.say("\n")
	}
	a.say("\n## Contexts\n\n")
	for _, c := range contexts {
		marker := " "
		if c.Slug == current {
			marker = "*"
		}
		a.say("%s %-24s %4d memories  expires %s\n", marker, c.Slug, c.MemoryCount, formatExpiry(c.ExpiresAt))
	}
	if len(memories) > 0 {
		a.say("\n## Memories in %s\n\n", current)
		for _, m := range memories {
			a.say("%-40s %-12s v%d\n", m.Name, m.ID, m.Version)
		}
	}
	return ExitOK, nil
}

func upsertCommand() command {
	return command{
		name:  "upsert",
		usage: "mem upsert [--context C] [--name N] [--id ID] [--tag T]... [--append] [--if-version V] [--by WHO] [body | -]",
		parse: parseUpsert,
		run:   runUpsert,
	}
}

type tagList []string

func (t *tagList) String() string { return strings.Join(*t, ",") }
func (t *tagList) Set(v string) error {
	*t = append(*t, v)
	return nil
}

type upsertFlags struct {
	context   string
	name      string
	id        string
	by        string
	append    bool
	ifVersion int
	ifSet     bool
	tags      tagList
	pos       []string
}

func parseUpsert(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	f := &upsertFlags{}
	fs.StringVar(&f.context, "context", "", "context slug (default global)")
	fs.StringVar(&f.name, "name", "", "memory name")
	fs.StringVar(&f.id, "id", "", "memory id to update")
	fs.StringVar(&f.by, "by", "", "who is writing")
	fs.BoolVar(&f.append, "append", false, "append to the existing body")
	fs.IntVar(&f.ifVersion, "if-version", 0, "fail unless the memory is at this version")
	fs.Var(&f.tags, "tag", "add a tag (repeatable)")
	pos, err := collectArgs(fs, args)
	if err != nil {
		return nil, err
	}
	f.ifSet = flagWasSet(fs, "if-version")
	f.pos = pos
	a.state = f
	return nil, nil
}

func runUpsert(a *app, _ []string) (int, error) {
	f := a.state.(*upsertFlags)
	if len(f.pos) > 1 {
		return ExitUsage, usagef("mem upsert takes one body argument (or \"-\" for stdin)")
	}
	if f.ifSet && f.ifVersion < 1 {
		return ExitUsage, usagef("--if-version must be a positive version number")
	}

	body, fromStdin, err := readBody(a, f.pos)
	if err != nil {
		return ExitError, err
	}

	eng, err := a.openEngine()
	if err != nil {
		return ExitError, err
	}
	defer eng.Close()
	a.warnSetupNeeded()

	in := store.UpsertInput{
		Context:   f.context,
		ID:        f.id,
		Name:      f.name,
		Body:      body,
		UpdatedBy: firstNonEmpty(f.by, "cli"),
		Tags:      f.tags,
		Append:    f.append,
	}
	if f.ifSet {
		v := f.ifVersion
		in.IfVersion = &v
	}
	m, err := eng.Store.Upsert(in)
	if err != nil {
		return ExitError, err
	}
	warnings := withSidecarHintAll(eng.Warnings())
	if err := a.emit(map[string]any{
		"id":         m.ID,
		"context":    m.Context,
		"name":       m.Name,
		"version":    m.Version,
		"updated_at": m.UpdatedAt.UTC(),
		"updated_by": m.UpdatedBy,
		"tags":       m.Tags,
		"warnings":   warnings,
	}); err != nil {
		return ExitError, err
	}
	a.say("%s v%d (%s/%s)\n", m.ID, m.Version, m.Context, m.Name)
	if fromStdin && !a.globals.json && a.globals.verbose {
		a.hint("read %d bytes from stdin\n", len(body))
	}
	return ExitOK, nil
}

// readBody resolves the body from an argument, "-", or piped stdin.
func readBody(a *app, pos []string) (string, bool, error) {
	if len(pos) == 1 && pos[0] != "-" {
		return pos[0], false, nil
	}
	data, err := readAll(a.io.In)
	if err != nil {
		return "", false, fmt.Errorf("read body from stdin: %w", err)
	}
	return string(data), true, nil
}

func deleteCommand() command {
	return command{
		name:  "delete",
		usage: "mem delete <id>",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			return collectArgs(fs, args)
		},
		run: runDelete,
	}
}

func runDelete(a *app, pos []string) (int, error) {
	if len(pos) != 1 {
		return ExitUsage, usagef("usage: mem delete <id>")
	}
	eng, err := a.openEngineReadOnly()
	if err != nil {
		return ExitError, err
	}
	defer eng.Close()
	// Deletion needs no embeddings, but the hook keeps the index consistent.
	if err := eng.Store.Delete(pos[0]); err != nil {
		return ExitError, err
	}
	if err := a.emit(map[string]any{"deleted": pos[0]}); err != nil {
		return ExitError, err
	}
	a.say("deleted %s\n", pos[0])
	return ExitOK, nil
}

func searchCommand() command {
	return command{
		name:  "search",
		usage: "mem search <query...> [--context C] [--no-global] [--limit N] [--per-doc N] [--fuzzy] [--no-fuzzy]",
		parse: parseSearch,
		run:   runSearch,
	}
}

type searchFlags struct {
	context    string
	noGlobal   bool
	limit      int
	perDoc     int
	fuzzy      bool
	noFuzzy    bool
	fuzzySet   bool
	noFuzzySet bool
	pos        []string
}

func parseSearch(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
	f := &searchFlags{}
	fs.StringVar(&f.context, "context", "", "context slug to search with global")
	fs.BoolVar(&f.noGlobal, "no-global", false, "exclude the global context")
	fs.IntVar(&f.limit, "limit", 8, "maximum hits")
	fs.IntVar(&f.perDoc, "per-doc", 1, "maximum hits per memory")
	fs.BoolVar(&f.fuzzy, "fuzzy", false, "enable fuzzy and partial-word matching")
	fs.BoolVar(&f.noFuzzy, "no-fuzzy", false, "disable fuzzy and partial-word matching")
	pos, err := collectArgs(fs, args)
	if err != nil {
		return nil, err
	}
	f.fuzzySet = flagWasSet(fs, "fuzzy")
	f.noFuzzySet = flagWasSet(fs, "no-fuzzy")
	f.pos = pos
	a.state = f
	return nil, nil
}

func isFuzzyOnly(sources []string) bool {
	for _, s := range sources {
		if s == "bm25" || s == "vec" || s == "rerank" {
			return false
		}
	}
	return true
}

func runSearch(a *app, _ []string) (int, error) {
	f := a.state.(*searchFlags)
	if f.fuzzySet && f.noFuzzySet {
		return ExitUsage, usagef("cannot use both --fuzzy and --no-fuzzy")
	}
	query := strings.TrimSpace(strings.Join(f.pos, " "))
	if query == "" {
		return ExitUsage, usagef("usage: mem search <query...>")
	}
	if f.limit < 1 {
		return ExitUsage, usagef("--limit must be at least 1")
	}
	if f.perDoc < 1 {
		return ExitUsage, usagef("--per-doc must be at least 1")
	}
	eng, err := a.openEngine()
	if err != nil {
		return ExitError, err
	}
	defer eng.Close()
	a.warnSetupNeeded()

	res, err := eng.Search(a.context(), searchRequest(f, query))
	if err != nil {
		return ExitError, err
	}

	if a.globals.json {
		hits := make([]map[string]any, 0, len(res.Hits))
		for _, h := range res.Hits {
			matchedTerms := h.MatchedTerms
			if matchedTerms == nil {
				matchedTerms = []string{}
			}
			sources := h.Sources
			if sources == nil {
				sources = []string{}
			}
			hits = append(hits, map[string]any{
				"id":            h.DocID,
				"context":       h.Context,
				"name":          h.Name,
				"heading":       h.Heading,
				"snippet":       h.Snippet,
				"chunk":         h.Chunk,
				"score":         h.Score,
				"sources":       sources,
				"matched_terms": matchedTerms,
			})
		}
		return ExitOK, a.emit(map[string]any{
			"hits":     hits,
			"degraded": withSidecarHintAll(res.Degraded),
			"took_ms":  float64(res.Took.Microseconds()) / 1000.0,
		})
	}
	if len(res.Hits) == 0 {
		a.say("no matches\n")
	} else {
		for i, h := range res.Hits {
			marker := ""
			if isFuzzyOnly(h.Sources) {
				marker = "~ "
			}
			a.say("%s%d. %s (%s/%s) score=%s [%s]\n", marker, i+1, h.Name, h.Context, h.DocID,
				strconv.FormatFloat(h.Score, 'f', 4, 64), strings.Join(h.Sources, ","))
			if len(h.MatchedTerms) > 0 {
				a.say("   matched: %s\n", strings.Join(h.MatchedTerms, ", "))
			}
			if h.Snippet != "" {
				a.say("   %s\n", indentContinuation(h.Snippet))
			}
		}
	}
	for _, d := range withSidecarHintAll(res.Degraded) {
		fmt.Fprintf(a.io.Err, "note: %s\n", d)
	}
	return ExitOK, nil
}

func indentContinuation(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n   ")
}

func syncCommand() command {
	return command{
		name:  "sync",
		usage: "mem sync",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			pos, err := collectArgs(fs, args)
			return pos, err
		},
		run: runSync,
	}
}

func runSync(a *app, pos []string) (int, error) {
	if len(pos) > 0 {
		return ExitUsage, usagef("mem sync takes no arguments")
	}
	// Progress goes to stderr so stdout stays clean for --json.
	progress := engine.WithSyncProgress(func(p engine.SyncProgress) {
		if a.globals.json || a.globals.quiet {
			return
		}
		fmt.Fprintf(a.io.Err, "\r%s %d/%d %s/%s", p.Phase, p.Done, p.Total, p.Context, p.DocID)
		if p.Done == p.Total {
			fmt.Fprintln(a.io.Err)
		}
	})
	eng, err := a.openEngineWith(progress)
	if err != nil {
		return ExitError, err
	}
	defer eng.Close()

	rep, err := eng.Sync(a.context())
	if err != nil {
		return ExitError, err
	}
	if err := a.emit(map[string]any{
		"total":     rep.Total,
		"added":     rep.Added,
		"updated":   rep.Updated,
		"removed":   rep.Removed,
		"unchanged": rep.Unchanged,
		"indexed":   rep.Indexed,
		"filled":    rep.Filled,
		"took_ms":   float64(rep.Duration.Microseconds()) / 1000.0,
	}); err != nil {
		return ExitError, err
	}
	a.say("synced %d documents: %d added, %d updated, %d removed, %d unchanged, %d pending vectors filled (%.1fs)\n",
		rep.Total, rep.Added, rep.Updated, rep.Removed, rep.Unchanged, rep.Filled, rep.Duration.Seconds())
	return ExitOK, nil
}

func statusCommand() command {
	return command{
		name:  "status",
		usage: "mem status",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			return collectArgs(fs, args)
		},
		run: runStatus,
	}
}

func runStatus(a *app, pos []string) (int, error) {
	if len(pos) > 0 {
		return ExitUsage, usagef("mem status takes no arguments")
	}
	if err := a.loadConfig(); err != nil {
		return ExitError, err
	}
	cfg := a.cfg
	embedModel := filepathJoin(cfg.ModelsDir, firstNonEmpty(cfg.EmbedModel, config.DefaultEmbedModel))
	rerankModel := filepathJoin(cfg.ModelsDir, firstNonEmpty(cfg.RerankModel, config.DefaultRerankModel))
	sidecar := DetectSidecar(cfg)

	// Cheap, offline integrity checks: no model is started, no binary executed.
	// This is what catches a stub or wrong-architecture llama-server before a
	// search fails with an opaque OS error.
	embedStatus := setup.CheckModel(embedModel, setup.EmbeddingModel)
	rerankStatus := setup.CheckModel(rerankModel, setup.RerankModel)
	sidecarStatus := setup.CheckSidecar(sidecar)

	var (
		contexts, memories, indexedDocs int
		inSync                          bool
		engineErr                       string
		warnings                        []string
	)
	eng, err := a.openEngineReadOnly()
	if err != nil {
		engineErr = err.Error()
	} else {
		ctxs, err := eng.Store.ListContexts()
		if err == nil {
			contexts = len(ctxs)
			for _, c := range ctxs {
				list, lerr := eng.Store.List(c.Slug)
				if lerr == nil {
					memories += len(list)
				}
			}
		}
		if state, serr := eng.Index.State(); serr == nil {
			indexedDocs = len(state)
		}
		// A read-only drift check: compare store docs with index state without
		// indexing anything, so status never starts a model.
		inSync = indexInSync(eng)
		warnings = eng.Warnings()
		eng.Close()
	}

	payload := map[string]any{
		"version":         Version,
		"config_path":     a.cfgPath,
		"data_dir":        cfg.DataDir,
		"models_dir":      cfg.ModelsDir,
		"bin_dir":         cfg.BinDir(),
		"port":            cfg.Port,
		"embed_model":     embedModel,
		"embed_present":   embedStatus.OK,
		"embed_problem":   embedStatus.Problem,
		"rerank":          cfg.Rerank,
		"rerank_model":    rerankModel,
		"rerank_present":  rerankStatus.OK,
		"rerank_problem":  rerankStatus.Problem,
		"sidecar":         sidecarStatus.Path,
		"sidecar_ok":      sidecarStatus.OK,
		"sidecar_problem": sidecarStatus.Problem,
		"sidecar_asset":   sidecarStatus.Asset,
		"sidecar_size":    sidecarStatus.Size,
		"needs_reinstall": needsReinstall(embedStatus, rerankStatus, sidecarStatus, cfg),
		"contexts":        contexts,
		"memories":        memories,
		"indexed_docs":    indexedDocs,
		"in_sync":         inSync,
		"warnings":        warnings,
		"error":           engineErr,
	}
	if a.globals.json {
		return ExitOK, a.emit(payload)
	}
	a.say("mem %s\n", Version)
	a.say("config      %s\n", a.cfgPath)
	a.say("data dir    %s\n", cfg.DataDir)
	a.say("contexts    %d\n", contexts)
	a.say("memories    %d\n", memories)
	a.say("indexed     %d documents (%s)\n", indexedDocs, syncWord(inSync))
	markStatus(a, embedStatus.OK, "embedding", embedModel, embedStatus.Problem)
	markStatus(a, sidecarStatus.OK, "llama-server", firstNonEmpty(sidecarStatus.Path, "(not found)"), sidecarStatus.Problem)
	if cfg.Rerank {
		markStatus(a, rerankStatus.OK, "reranker", rerankModel, rerankStatus.Problem)
	} else {
		a.say("reranker    off (opt-in)\n")
	}
	if needsReinstall(embedStatus, rerankStatus, sidecarStatus, cfg) {
		a.say("fix         run `mem init --force` to reinstall the llama-server and models\n")
	}
	for _, w := range warnings {
		a.say("warning     %s\n", w)
	}
	if engineErr != "" {
		a.say("error       %s\n", engineErr)
	}
	return ExitOK, nil
}

// needsReinstall reports whether an integrity check found something that
// `mem init --force` should repair.
func needsReinstall(embed, rerank setup.ModelStatus, sidecar setup.SidecarStatus, cfg config.Config) bool {
	if sidecar.NeedsReinstall {
		return true
	}
	// A model that is present but broken is worth reinstalling; an absent
	// optional reranker is not.
	if embed.Present && !embed.OK {
		return true
	}
	if cfg.Rerank && rerank.Present && !rerank.OK {
		return true
	}
	return false
}

// markStatus prints an ok/missing/broken marker for one component plus the
// reason behind a failure. "broken" means the file is present but unusable,
// which is what a stub or wrong-architecture binary looks like.
func markStatus(a *app, ok bool, label, target, problem string) {
	switch {
	case ok:
		a.say("%-11s %s\n", "ok", label+" "+target)
	case problem != "":
		a.say("%-11s %s %s  (%s)\n", "broken", label, target, problem)
	default:
		a.say("%-11s %s\n", "missing", label+" "+target)
	}
}

func syncWord(ok bool) string {
	if ok {
		return "in sync"
	}
	return "out of sync, run `mem sync`"
}

func configCommand() command {
	return command{
		name:  "config",
		usage: "mem config [key] [value]",
		parse: func(fs *flag.FlagSet, a *app, args []string) ([]string, error) {
			return collectArgs(fs, args)
		},
		run: runConfig,
	}
}

func runConfig(a *app, pos []string) (int, error) {
	if err := a.loadConfig(); err != nil {
		return ExitError, err
	}
	switch len(pos) {
	case 0:
		if a.globals.json {
			out := map[string]any{}
			for _, k := range config.Keys() {
				v, _ := a.cfg.Get(k)
				out[k] = v
			}
			return ExitOK, a.emit(out)
		}
		for _, k := range config.Keys() {
			v, _ := a.cfg.Get(k)
			a.say("%-24s %s\n", k, v)
		}
		return ExitOK, nil
	case 1:
		v, err := a.cfg.Get(pos[0])
		if err != nil {
			return ExitUsage, usagef("%v", err)
		}
		if a.globals.json {
			return ExitOK, a.emit(map[string]string{pos[0]: v})
		}
		a.say("%s\n", v)
		return ExitOK, nil
	default:
		key, value := pos[0], strings.Join(pos[1:], " ")
		if err := a.cfg.Set(key, value); err != nil {
			return ExitUsage, usagef("%v", err)
		}
		if err := a.cfg.SaveFile(a.cfgPath); err != nil {
			return ExitError, err
		}
		if a.globals.json {
			return ExitOK, a.emit(map[string]string{"key": key, "value": value})
		}
		a.say("%s = %s\n", key, value)
		return ExitOK, nil
	}
}
