package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"memory/internal/llm"
)

type VectorSearchMode int

const (
	VectorSearchAuto VectorSearchMode = iota
	VectorSearchExact
	VectorSearchTwoStage
)

type Query struct {
	Text       string
	Contexts   []string
	Limit      int
	NoRerank   bool
	PerDoc     int
	VectorMode VectorSearchMode
	Fuzzy      *bool
}

type Hit struct {
	DocID        string   `json:"doc_id"`
	Context      string   `json:"context"`
	Name         string   `json:"name"`
	Heading      string   `json:"heading"`
	Snippet      string   `json:"snippet"`
	Chunk        int      `json:"chunk"`
	Score        float64  `json:"score"`
	Sources      []string `json:"sources"`
	MatchedTerms []string `json:"matched_terms"`
}

type SearchTimings struct {
	EmbedQuery  time.Duration `json:"embed_query"`
	VecKNN      time.Duration `json:"vec_knn"`
	FTS         time.Duration `json:"fts"`
	Fusion      time.Duration `json:"fusion"`
	Hydration   time.Duration `json:"hydration"`
	Rerank      time.Duration `json:"rerank"`
	Unaccounted time.Duration `json:"unaccounted"`
	Total       time.Duration `json:"total"`
}

type Result struct {
	Hits     []Hit         `json:"hits"`
	Degraded []string      `json:"degraded,omitempty"`
	Took     time.Duration `json:"took"`
	Timings  SearchTimings `json:"timings"`
}

func (ix *Index) Search(ctx context.Context, q Query) (Result, error) {
	startTime := time.Now()

	if len(q.Contexts) == 0 {
		return Result{}, errors.New("search: Contexts cannot be empty")
	}

	if q.Limit <= 0 {
		q.Limit = 8
	}
	if q.PerDoc <= 0 {
		q.PerDoc = 1
	}

	// Check for external updates from other connections before acquiring read lock
	_ = ix.checkCrossProcessSync(ctx)

	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if ix.closed {
		return Result{}, errors.New("search: index is closed")
	}

	var timings SearchTimings
	var timingsMu sync.Mutex

	var degraded []string
	var degMu sync.Mutex
	addDegraded := func(msg string) {
		degMu.Lock()
		degraded = append(degraded, msg)
		degMu.Unlock()
	}

	if ix.capExceeded {
		addDegraded("resident vector cap exceeded; using SQLite path")
	}

	var vecRanks = make(map[int64]int)
	var bm25Ranks = make(map[int64]int)
	var bm25Scores = make(map[int64]float64)

	var vecErr error
	var bm25Err error

	fuzzyEnabled := ix.fuzzyDefault
	if q.Fuzzy != nil {
		fuzzyEnabled = *q.Fuzzy
	}

	var fuzzyRanks = make(map[int64]int)
	var fuzzyExpansions []string
	var nameRanks = make(map[int64]int)
	var nameTermsMap = make(map[int64][]string)

	if fuzzyEnabled {
		if ix.vocabCache != nil {
			fuzzyExpansions = ix.generateFuzzyExpansions(q.Text)
		}
		if ix.docNames != nil {
			nameMatches := ix.docNames.Search(q.Text, q.Contexts, 50)
			for r, nm := range nameMatches {
				nameRanks[nm.chunkID] = r + 1
				nameTermsMap[nm.chunkID] = nm.matchedTerms
			}
		}
	}

	var wg sync.WaitGroup
	legsCount := 2
	runFuzzyFTS := fuzzyEnabled && len(fuzzyExpansions) > 0
	if runFuzzyFTS {
		legsCount = 3
	}
	wg.Add(legsCount)

	const ftsSQL = `
		SELECT rowid, bm25(chunks_fts)
		FROM chunks_fts
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 50
	`

	// Leg 1: Vector search
	go func() {
		defer wg.Done()

		if strings.TrimSpace(q.Text) == "" {
			return
		}

		tEmbedStart := time.Now()
		fvecs, err := ix.embedder.Embed(ctx, llm.KindQuery, []string{q.Text})
		tEmbedDuration := time.Since(tEmbedStart)
		timingsMu.Lock()
		timings.EmbedQuery = tEmbedDuration
		timingsMu.Unlock()

		if err != nil {
			vecErr = fmt.Errorf("embed query: %w", err)
			addDegraded("embedder unavailable: " + err.Error())
			return
		}
		if len(fvecs) == 0 || len(fvecs[0]) != ModelDim {
			vecErr = fmt.Errorf("invalid query vector length")
			addDegraded("embedder returned invalid vector")
			return
		}

		qVec := Quantize(fvecs[0])

		totalChunks := ix.TotalChunksInContexts(q.Contexts)
		useTwoStage := false
		if q.VectorMode == VectorSearchTwoStage {
			useTwoStage = true
		} else if q.VectorMode == VectorSearchAuto && totalChunks >= ix.twoStageThreshold {
			useTwoStage = true
		}

		tKNNStart := time.Now()

		// Preferred path: in-RAM vector store (eliminates SQLite WASM overhead and full-scans)
		if ix.vecStore != nil && ix.IsVecStoreReady() && ix.vecStore.Count() > 0 {
			var hits []knnCandidate
			if useTwoStage {
				qBit := QuantizeBit(fvecs[0])
				hits = ix.vecStore.SearchTwoStage(qVec, qBit, q.Contexts, 50, 200)
			} else {
				hits = ix.vecStore.SearchExact(qVec, q.Contexts, 50)
			}
			for rank, h := range hits {
				vecRanks[h.rowid] = rank + 1
			}
			timingsMu.Lock()
			timings.VecKNN = time.Since(tKNNStart)
			timingsMu.Unlock()
			return
		}

		// Fallback: SQLite virtual table query
		if useTwoStage {
			// Stage 1: bit Hamming top-200
			qBit := QuantizeBit(fvecs[0])
			placeholders := make([]string, len(q.Contexts))
			argsBit := make([]interface{}, 1+len(q.Contexts))
			argsBit[0] = qBit
			for i, c := range q.Contexts {
				placeholders[i] = "?"
				argsBit[1+i] = c
			}

			bitSQL := fmt.Sprintf(`
				SELECT rowid
				FROM chunks_vec_bit
				WHERE embedding MATCH vec_bit(?) AND k = 200 AND context IN (%s)
			`, strings.Join(placeholders, ", "))

			rowsBit, err := ix.db.QueryContext(ctx, bitSQL, argsBit...)
			if err != nil {
				vecErr = fmt.Errorf("two-stage bit knn query: %w", err)
				addDegraded("two-stage bit search failed: " + err.Error())
				return
			}
			var candidateIDs []int64
			for rowsBit.Next() {
				var rid int64
				if err := rowsBit.Scan(&rid); err == nil {
					candidateIDs = append(candidateIDs, rid)
				}
			}
			rowsBit.Close()

			if len(candidateIDs) == 0 {
				timingsMu.Lock()
				timings.VecKNN = time.Since(tKNNStart)
				timingsMu.Unlock()
				return
			}

			// Stage 2: rescore candidates with int8 L2 distance
			rescorePlaceholders := make([]string, len(candidateIDs))
			rescoreArgs := make([]interface{}, 1+len(candidateIDs))
			rescoreArgs[0] = qVec
			for i, rid := range candidateIDs {
				rescorePlaceholders[i] = "?"
				rescoreArgs[1+i] = rid
			}

			rescoreSQL := fmt.Sprintf(`
				SELECT rowid, distance
				FROM chunks_vec
				WHERE embedding MATCH vec_int8(?) AND k = 50 AND rowid IN (%s)
			`, strings.Join(rescorePlaceholders, ", "))

			rowsRescore, err := ix.db.QueryContext(ctx, rescoreSQL, rescoreArgs...)
			if err != nil {
				vecErr = fmt.Errorf("two-stage rescore query: %w", err)
				addDegraded("two-stage rescore failed: " + err.Error())
				return
			}
			defer rowsRescore.Close()

			rank := 1
			for rowsRescore.Next() {
				var rowid int64
				var dist float64
				if err := rowsRescore.Scan(&rowid, &dist); err == nil {
					vecRanks[rowid] = rank
					rank++
				}
			}
			timingsMu.Lock()
			timings.VecKNN = time.Since(tKNNStart)
			timingsMu.Unlock()
		} else {
			// Exact int8 KNN
			placeholders := make([]string, len(q.Contexts))
			args := make([]interface{}, 1+len(q.Contexts))
			args[0] = qVec
			for i, c := range q.Contexts {
				placeholders[i] = "?"
				args[1+i] = c
			}

			querySQL := fmt.Sprintf(`
				SELECT rowid, distance
				FROM chunks_vec
				WHERE embedding MATCH vec_int8(?) AND k = 50 AND context IN (%s)
			`, strings.Join(placeholders, ", "))

			rows, err := ix.db.QueryContext(ctx, querySQL, args...)
			if err != nil {
				vecErr = fmt.Errorf("knn query: %w", err)
				addDegraded("vector search failed: " + err.Error())
				return
			}
			defer rows.Close()

			rank := 1
			for rows.Next() {
				var rowid int64
				var dist float64
				if err := rows.Scan(&rowid, &dist); err == nil {
					vecRanks[rowid] = rank
					rank++
				}
			}
			timingsMu.Lock()
			timings.VecKNN = time.Since(tKNNStart)
			timingsMu.Unlock()
		}
	}()

	// Leg 2: BM25 search top 50
	go func() {
		defer wg.Done()

		tFTSStart := time.Now()

		// 1. Try AND conjunction first for fast, highly-precise hits
		andQuery := BuildFTS5ANDQuery(q.Text, q.Contexts)
		if andQuery != "" {
			rows, err := ix.db.QueryContext(ctx, ftsSQL, andQuery)
			if err == nil {
				rank := 1
				for rows.Next() {
					var rowid int64
					var score float64
					if err := rows.Scan(&rowid, &score); err == nil {
						bm25Ranks[rowid] = rank
						bm25Scores[rowid] = score
						rank++
					}
				}
				rows.Close()
				if len(bm25Ranks) >= 5 {
					// Sufficient high-precision AND hits found! Done!
					timingsMu.Lock()
					timings.FTS = time.Since(tFTSStart)
					timingsMu.Unlock()
					return
				}
			}
		}

		// 2. Fallback to capped OR query (rarest 8 terms)
		orQuery := BuildFTS5CappedORQuery(q.Text, q.Contexts, 8)
		if orQuery == "" || orQuery == andQuery {
			timingsMu.Lock()
			timings.FTS = time.Since(tFTSStart)
			timingsMu.Unlock()
			return
		}

		// Reset ranks before fallback
		bm25Ranks = make(map[int64]int)
		bm25Scores = make(map[int64]float64)

		rows, err := ix.db.QueryContext(ctx, ftsSQL, orQuery)
		if err != nil {
			timingsMu.Lock()
			timings.FTS = time.Since(tFTSStart)
			timingsMu.Unlock()
			bm25Err = fmt.Errorf("bm25 query: %w", err)
			addDegraded("bm25 search failed: " + err.Error())
			return
		}
		defer rows.Close()

		rank := 1
		for rows.Next() {
			var rowid int64
			var score float64
			if err := rows.Scan(&rowid, &score); err == nil {
				bm25Ranks[rowid] = rank
				bm25Scores[rowid] = score
				rank++
			}
		}
		timingsMu.Lock()
		timings.FTS = time.Since(tFTSStart)
		timingsMu.Unlock()
	}()

	// Leg 3: Fuzzy FTS expansions search
	if runFuzzyFTS {
		go func() {
			defer wg.Done()
			fuzzyQuery := BuildFuzzyQuery(fuzzyExpansions, q.Contexts)
			if fuzzyQuery == "" {
				return
			}
			rows, err := ix.db.QueryContext(ctx, ftsSQL, fuzzyQuery)
			if err != nil {
				return
			}
			defer rows.Close()

			rank := 1
			for rows.Next() {
				var rowid int64
				var score float64
				if err := rows.Scan(&rowid, &score); err == nil {
					fuzzyRanks[rowid] = rank
					rank++
				}
			}
		}()
	}

	wg.Wait()

	// Check if all legs failed
	if vecErr != nil && bm25Err != nil && len(fuzzyRanks) == 0 && len(nameRanks) == 0 {
		timings.Total = time.Since(startTime)
		return Result{Degraded: degraded, Took: time.Since(startTime), Timings: timings}, fmt.Errorf("both search legs failed: vec=(%v), bm25=(%v)", vecErr, bm25Err)
	}

	// Fuse rankings
	tFusionStart := time.Now()
	var merged map[int64]*candidateHit
	if fuzzyEnabled {
		merged = ConfidenceRRFWithFuzzy(vecRanks, bm25Ranks, bm25Scores, fuzzyRanks, nameRanks, q.Text, ix.boostAmount, ix.lowWeight, 0.04, 0.05)
	} else {
		switch ix.fusionMode {
		case FusionModeConfidence:
			merged = ConfidenceRRF(vecRanks, bm25Ranks, bm25Scores, q.Text, ix.boostAmount, ix.lowWeight)
		case FusionModeWeightedRRF:
			merged = WeightedRRF(vecRanks, bm25Ranks, ix.wVec, ix.wBM25, ix.k)
		case FusionModeBoost:
			merged = BoostFusion(vecRanks, bm25Ranks, ix.boostThreshold, ix.boostAmount)
		default:
			merged = ConfidenceRRF(vecRanks, bm25Ranks, bm25Scores, q.Text, ix.boostAmount, ix.lowWeight)
		}
	}
	timings.Fusion = time.Since(tFusionStart)

	if len(merged) == 0 {
		timings.Total = time.Since(startTime)
		return Result{Hits: []Hit{}, Degraded: degraded, Took: time.Since(startTime), Timings: timings}, nil
	}

	// Sort candidates by score descending
	candidates := make([]*candidateHit, 0, len(merged))
	for _, h := range merged {
		candidates = append(candidates, h)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Fetch chunk metadata for top 50 candidates
	if len(candidates) > 50 {
		candidates = candidates[:50]
	}

	tHydrationStart := time.Now()
	chunkIDs := make([]interface{}, len(candidates))
	placeholders := make([]string, len(candidates))
	candMap := make(map[int64]*candidateHit, len(candidates))
	for i, c := range candidates {
		chunkIDs[i] = c.chunkID
		placeholders[i] = "?"
		candMap[c.chunkID] = c
	}

	metaSQL := fmt.Sprintf(`
		SELECT c.id, c.doc_id, c.context, d.name, c.ord, c.heading, c.text
		FROM chunks c
		JOIN docs d ON c.doc_id = d.id
		WHERE c.id IN (%s)
	`, strings.Join(placeholders, ", "))

	rows, err := ix.db.QueryContext(ctx, metaSQL, chunkIDs...)
	if err != nil {
		timings.Total = time.Since(startTime)
		return Result{Degraded: degraded, Took: time.Since(startTime), Timings: timings}, fmt.Errorf("fetch chunk details: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int64
		var docID, ctxName, docName, heading, text string
		var ord int
		if err := rows.Scan(&cid, &docID, &ctxName, &docName, &ord, &heading, &text); err == nil {
			if h, ok := candMap[cid]; ok {
				h.docID = docID
				h.context = ctxName
				h.name = docName
				h.heading = heading
				h.text = text
				h.chunk = ord
			}
		}
	}

	validCandidates := make([]*candidateHit, 0, len(candidates))
	for _, c := range candidates {
		if c.docID != "" {
			validCandidates = append(validCandidates, c)
		}
	}
	candidates = validCandidates

	// Populate matched_terms for highlighting (up to 8 unique terms)
	queryWords := strings.Fields(strings.ToLower(q.Text))
	for _, c := range validCandidates {
		var terms []string
		seenTerm := make(map[string]bool)
		addTerm := func(t string) {
			t = strings.TrimSpace(t)
			if t == "" || seenTerm[strings.ToLower(t)] {
				return
			}
			seenTerm[strings.ToLower(t)] = true
			terms = append(terms, t)
		}

		lowerAll := strings.ToLower(c.name + " " + c.heading + " " + c.text)

		// 1. Exact query terms
		for _, qw := range queryWords {
			cl := CleanQueryToken(qw)
			if len(cl) >= 2 && strings.Contains(lowerAll, cl) {
				addTerm(cl)
			}
		}

		// 2. Name leg matched terms
		if nTerms, ok := nameTermsMap[c.chunkID]; ok {
			for _, nt := range nTerms {
				addTerm(nt)
			}
		}

		// 3. Fuzzy expansions matched
		for _, exp := range fuzzyExpansions {
			if strings.Contains(lowerAll, exp) {
				addTerm(exp)
				if len(terms) >= 8 {
					break
				}
			}
		}

		if len(terms) > 8 {
			terms = terms[:8]
		}
		c.matchedTerms = terms
	}

	hydrationPart1 := time.Since(tHydrationStart)

	// Reranking top 30 (only if reranker was provided to OpenIndex)
	tRerankStart := time.Now()
	if ix.reranker != nil && !q.NoRerank && len(candidates) > 0 && strings.TrimSpace(q.Text) != "" {
		rerankLimit := 30
		if len(candidates) < rerankLimit {
			rerankLimit = len(candidates)
		}

		rerankDocs := make([]string, rerankLimit)
		for i := 0; i < rerankLimit; i++ {
			rerankDocs[i] = candidates[i].text
		}

		scores, err := ix.reranker.Rerank(ctx, q.Text, rerankDocs)
		if err != nil {
			addDegraded("reranker unavailable: " + err.Error())
		} else if len(scores) == rerankLimit {
			for i := 0; i < rerankLimit; i++ {
				candidates[i].score = scores[i]
				candidates[i].sources = append(candidates[i].sources, "rerank")
			}
			sort.Slice(candidates[:rerankLimit], func(i, j int) bool {
				return candidates[i].score > candidates[j].score
			})
		}
	}
	timings.Rerank = time.Since(tRerankStart)

	// Apply PerDoc collapse and Limit
	tSnippetStart := time.Now()
	docCounts := make(map[string]int)
	var finalHits []Hit

	for _, c := range candidates {
		count := docCounts[c.docID]
		if count >= q.PerDoc {
			continue
		}
		docCounts[c.docID] = count + 1

		matchedTerms := c.matchedTerms
		if matchedTerms == nil {
			matchedTerms = []string{}
		}
		finalHits = append(finalHits, Hit{
			DocID:        c.docID,
			Context:      c.context,
			Name:         c.name,
			Heading:      c.heading,
			Snippet:      MakeSnippet(c.text, q.Text, 500),
			Chunk:        c.chunk,
			Score:        c.score,
			Sources:      c.sources,
			MatchedTerms: matchedTerms,
		})

		if len(finalHits) >= q.Limit {
			break
		}
	}

	timings.Hydration = hydrationPart1 + time.Since(tSnippetStart)
	timings.Total = time.Since(startTime)

	parallelLegs := timings.EmbedQuery + timings.VecKNN
	if timings.FTS > parallelLegs {
		parallelLegs = timings.FTS
	}
	accounted := parallelLegs + timings.Fusion + timings.Hydration + timings.Rerank
	if timings.Total > accounted {
		timings.Unaccounted = timings.Total - accounted
	} else {
		timings.Unaccounted = 0
	}

	return Result{
		Hits:     finalHits,
		Degraded: degraded,
		Took:     time.Since(startTime),
		Timings:  timings,
	}, nil
}

// BuildFuzzyQuery builds the secondary FTS5 MATCH query for fuzzy expansions.
func BuildFuzzyQuery(expansions []string, contexts []string) string {
	if len(expansions) == 0 {
		return ""
	}
	var scopeClause string
	if len(contexts) > 0 {
		scopeTokens := make([]string, len(contexts))
		for i, ctx := range contexts {
			scopeTokens[i] = ScopeToken(ctx)
		}
		if len(scopeTokens) == 1 {
			scopeClause = fmt.Sprintf("scope: %s", scopeTokens[0])
		} else {
			scopeClause = fmt.Sprintf("scope: (%s)", strings.Join(scopeTokens, " OR "))
		}
	}

	selected := make([]string, len(expansions))
	for i, exp := range expansions {
		selected[i] = fmt.Sprintf("%q", exp)
	}

	textClause := "(" + strings.Join(selected, " OR ") + ")"
	if scopeClause == "" {
		return textClause
	}
	return scopeClause + " AND " + textClause
}

func (ix *Index) generateFuzzyExpansions(queryText string) []string {
	fields := strings.Fields(queryText)
	if len(fields) == 0 {
		return nil
	}

	var allExpansions []string
	seen := make(map[string]bool)

	exactTokens := make(map[string]bool)
	for _, f := range fields {
		cl := strings.ToLower(CleanQueryToken(f))
		if cl != "" {
			exactTokens[cl] = true
		}
	}

	for i, f := range fields {
		tok := strings.ToLower(CleanQueryToken(f))
		if len(tok) < 2 {
			continue
		}
		isLast := (i == len(fields)-1)
		isIdent := IsIdentifierLike(f)
		inVocab := ix.vocabCache.HasTerm(ix.db, tok)

		var tokenExpansions []string
		tokenSeen := make(map[string]bool)

		addExp := func(term string) {
			term = strings.ToLower(term)
			if term == "" || term == tok || exactTokens[term] || tokenSeen[term] || seen[term] {
				return
			}
			tokenSeen[term] = true
			seen[term] = true
			tokenExpansions = append(tokenExpansions, term)
		}

		if isLast {
			// Prefix match for last token (min 2 chars; search-as-you-type)
			prefixes := ix.vocabCache.LookupPrefix(ix.db, tok, 8)
			for _, p := range prefixes {
				addExp(p)
				if len(tokenExpansions) >= 8 {
					break
				}
			}
		} else if !inVocab {
			// Other tokens: prefix match only when not an exact vocabulary term
			prefixes := ix.vocabCache.LookupPrefix(ix.db, tok, 8)
			for _, p := range prefixes {
				addExp(p)
				if len(tokenExpansions) >= 8 {
					break
				}
			}
		}

		// Typo tolerance: tokens absent from vocabulary, length >= 4, NOT identifier-like
		if !inVocab && !isIdent && len(tok) >= 4 && len(tokenExpansions) < 8 {
			typos := ix.vocabCache.FindTypoCandidates(ix.db, tok, 8-len(tokenExpansions))
			for _, t := range typos {
				addExp(t)
				if len(tokenExpansions) >= 8 {
					break
				}
			}
		}

		if len(tokenExpansions) > 8 {
			tokenExpansions = tokenExpansions[:8]
		}
		allExpansions = append(allExpansions, tokenExpansions...)
		if len(allExpansions) >= 24 {
			allExpansions = allExpansions[:24]
			break
		}
	}

	return allExpansions
}
