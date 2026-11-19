package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"

	"memory/internal/llm"
)

type Doc struct {
	ID      string
	Context string
	Name    string
	Body    string
	Version int
	Tags    []string
}

type DocState struct {
	Context        string
	Version        int
	Hash           string
	PendingVectors int
	Pending        int
}

type Option func(*Index)

func WithRebuild() Option {
	return func(ix *Index) {
		ix.rebuild = true
	}
}

func WithChunkOpts(opts ChunkOpts) Option {
	return func(ix *Index) {
		ix.chunkOpts = opts
	}
}

func WithBatchCommitSize(n int) Option {
	return func(ix *Index) {
		if n > 0 {
			ix.batchCommitSize = n
		}
	}
}

func WithTwoStageThreshold(threshold int) Option {
	return func(ix *Index) {
		if threshold >= 0 {
			ix.twoStageThreshold = threshold
		}
	}
}

// WithConfidenceFusion configures confidence-aware reciprocal rank fusion.
func WithConfidenceFusion(boostAmount, lowWeight float64) Option {
	return func(ix *Index) {
		ix.fusionMode = FusionModeConfidence
		if boostAmount > 0 {
			ix.boostAmount = boostAmount
		}
		if lowWeight > 0 {
			ix.lowWeight = lowWeight
		}
	}
}

// WithWeightedRRF configures classic weighted reciprocal rank fusion.
func WithWeightedRRF(wVec, wBM25, k float64) Option {
	return func(ix *Index) {
		ix.fusionMode = FusionModeWeightedRRF
		ix.wVec = wVec
		ix.wBM25 = wBM25
		ix.k = k
	}
}

// WithBoostFusion configures vector-first ranking with BM25 boost.
func WithBoostFusion(threshold int, boost float64) Option {
	return func(ix *Index) {
		ix.fusionMode = FusionModeBoost
		if threshold > 0 {
			ix.boostThreshold = threshold
		}
		if boost > 0 {
			ix.boostAmount = boost
		}
	}
}

func WithFusionWeights(wVec, wBM25, k float64) Option {
	return func(ix *Index) {
		ix.fusionMode = FusionModeWeightedRRF
		ix.wVec = wVec
		ix.wBM25 = wBM25
		ix.k = k
	}
}

// WithResidentVectors enables in-RAM caching of quantized bit and int8 vectors (default false).
func WithResidentVectors(resident bool) Option {
	return func(ix *Index) {
		ix.residentVectors = resident
	}
}

// WithResidentVectorsSync specifies whether resident vector loading should block synchronously on OpenIndex (useful for tests/benchmarks).
func WithResidentVectorsSync(sync bool) Option {
	return func(ix *Index) {
		ix.residentVectorsSync = sync
	}
}

// WithResidentVectorCap sets the maximum number of chunks before staying on the SQLite path (default 400,000).
func WithResidentVectorCap(maxChunks int) Option {
	return func(ix *Index) {
		if maxChunks > 0 {
			ix.residentVectorCap = maxChunks
		}
	}
}

const DefaultMaxRetainedDeletions = 100000

// WithMaxRetainedDeletions configures the maximum deletion log entries retained in chunk_deletions (default 100,000).
func WithMaxRetainedDeletions(n int) Option {
	return func(ix *Index) {
		if n > 0 {
			ix.maxRetainedDeletions = n
		}
	}
}

// WithUpsertBatchSize configures the batch size for embedding texts in UpsertMany (default 16).
func WithUpsertBatchSize(n int) Option {
	return func(ix *Index) {
		if n > 0 {
			ix.upsertBatchSize = n
		}
	}
}

// WithUpsertConcurrency configures the number of parallel embedding workers in UpsertMany (default 4).
func WithUpsertConcurrency(n int) Option {
	return func(ix *Index) {
		if n > 0 {
			ix.upsertConcurrency = n
		}
	}
}

// WithContextualEmbed toggles contextual chunk text in embeddings and FTS (default true).
func WithContextualEmbed(enabled bool) Option {
	return func(ix *Index) {
		ix.disableContextualEmbed = !enabled
	}
}

// WithTier0FTS toggles indexing Tier-0 extractive metadata in FTS (default true).
func WithTier0FTS(enabled bool) Option {
	return func(ix *Index) {
		ix.disableTier0FTS = !enabled
	}
}

// WithAppendKeywordsToEmbed configures whether keywords are appended to embedded text (default false).
func WithAppendKeywordsToEmbed(enabled bool) Option {
	return func(ix *Index) {
		ix.appendKeywordsToEmbed = enabled
	}
}

// WithFuzzy configures whether fuzzy matching (prefix, typo, name leg) is enabled by default.
func WithFuzzy(enabled bool) Option {
	return func(ix *Index) {
		ix.fuzzyDefault = enabled
	}
}

type Index struct {
	db                  *sql.DB
	embedder            llm.Embedder
	reranker            llm.Reranker
	chunkOpts           ChunkOpts
	batchCommitSize     int
	twoStageThreshold   int
	fusionMode          FusionMode
	boostAmount         float64
	lowWeight           float64
	boostThreshold      int
	wVec                 float64
	wBM25                float64
	k                    float64
	contextCounts        map[string]int
	pendingCounts        map[string]int
	totalPending         int
	countsMu             sync.RWMutex
	vecStore             *vecStore
	residentVectors      bool
	residentVectorsSync  bool
	residentVectorCap    int
	maxRetainedDeletions int
	upsertBatchSize     int
	upsertConcurrency   int
	lastDataVersion     int64
	maxSeenRowID        int64
	lastSeenDeletionID  int64
	vecStoreReady       bool
	vecStoreReadyMu     sync.RWMutex
	capExceeded         bool
	rebuild             bool
	closed              bool
	mu                  sync.RWMutex
	fuzzyDefault        bool
	vocabCache          *VocabCache
	docNames            *DocNameStore
	disableContextualEmbed bool
	disableTier0FTS        bool
	appendKeywordsToEmbed bool
	dfSnapshot          map[string]int
	dfTotalDocs         int
	dfMu                sync.RWMutex
}

func OpenIndex(path string, e llm.Embedder, r llm.Reranker, opts ...Option) (*Index, error) {
	if e == nil {
		return nil, errors.New("embedder is required")
	}

	dsn := fmt.Sprintf("file:%s?_txlock=immediate", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite3 (%s): %w", path, err)
	}

	// Set required connection pragmas
	_, err = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA busy_timeout = 5000;
		PRAGMA synchronous = NORMAL;
		PRAGMA foreign_keys = ON;
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init db pragmas: %w", err)
	}

	ix := &Index{
		db:                  db,
		embedder:            e,
		reranker:            r,
		chunkOpts:           DefaultChunkOpts(),
		batchCommitSize:     5000,
		twoStageThreshold:   10000,
		fusionMode:          FusionModeConfidence,
		boostAmount:         0.25,
		lowWeight:           0.08,
		boostThreshold:      3,
		wVec:                1.0,
		wBM25:               0.10,
		k:                   10.0,
		contextCounts:        make(map[string]int),
		pendingCounts:        make(map[string]int),
		vecStore:             nil,
		residentVectors:      false,
		residentVectorCap:    400000,
		maxRetainedDeletions: DefaultMaxRetainedDeletions,
		upsertBatchSize:      16,
		upsertConcurrency:    4,
		fuzzyDefault:         true,
		vocabCache:           NewVocabCache(),
		docNames:             NewDocNameStore(),
	}

	for _, opt := range opts {
		opt(ix)
	}

	if err := initSchema(db, ix.rebuild); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	if err := checkMeta(db, ix.rebuild); err != nil {
		db.Close()
		return nil, err
	}

	_ = ix.docNames.LoadFromDB(db)

	// Initialize tracking state
	_ = db.QueryRow("PRAGMA data_version").Scan(&ix.lastDataVersion)
	_ = db.QueryRow("SELECT COALESCE(max(rowid), 0) FROM chunks_vec").Scan(&ix.maxSeenRowID)
	_ = db.QueryRow("SELECT COALESCE(max(id), 0) FROM chunk_deletions").Scan(&ix.lastSeenDeletionID)

	// Initialize cached per-context chunk counts and pending counts
	totalChunks := 0
	totalPending := 0
	rows, err := db.Query(`
		SELECT context, count(*), COALESCE(SUM(CASE WHEN vec_pending = 1 THEN 1 ELSE 0 END), 0)
		FROM chunks GROUP BY context
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ctxName string
			var count, pCount int
			if err := rows.Scan(&ctxName, &count, &pCount); err == nil {
				ix.contextCounts[ctxName] = count
				ix.pendingCounts[ctxName] = pCount
				totalChunks += count
				totalPending += pCount
			}
		}
	}
	ix.totalPending = totalPending

	// Initialize vecStore according to resident vectors policy
	if ix.residentVectors {
		if totalChunks > ix.residentVectorCap {
			ix.capExceeded = true
			ix.vecStore = nil
		} else {
			ix.vecStore = newVecStore()
			if ix.residentVectorsSync {
				_ = ix.vecStore.LoadFromDB(db)
				ix.setVecStoreReady(true)
			} else {
				go ix.populateVecStoreInBackground()
			}
		}
	} else {
		ix.vecStore = nil
	}

	return ix, nil
}

func (ix *Index) Close() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.closed {
		return nil
	}
	ix.closed = true
	ix.setVecStoreReady(false)
	return ix.db.Close()
}

func (ix *Index) populateVecStoreInBackground() {
	ix.mu.RLock()
	if ix.closed || ix.vecStore == nil {
		ix.mu.RUnlock()
		return
	}
	db := ix.db
	vs := ix.vecStore
	ix.mu.RUnlock()

	err := vs.LoadFromDB(db)
	if err == nil {
		ix.setVecStoreReady(true)
	}
}

// IsVecStoreReady returns true if resident in-RAM vectors are loaded and ready to serve queries.
func (ix *Index) IsVecStoreReady() bool {
	ix.vecStoreReadyMu.RLock()
	defer ix.vecStoreReadyMu.RUnlock()
	return ix.vecStoreReady
}

func (ix *Index) setVecStoreReady(ready bool) {
	ix.vecStoreReadyMu.Lock()
	ix.vecStoreReady = ready
	ix.vecStoreReadyMu.Unlock()
}

func (ix *Index) pruneDeletionsLocked(ctx context.Context) {
	limit := ix.maxRetainedDeletions
	if limit <= 0 {
		limit = DefaultMaxRetainedDeletions
	}
	var maxID, count int64
	err := ix.db.QueryRowContext(ctx, "SELECT COALESCE(max(id), 0), count(*) FROM chunk_deletions").Scan(&maxID, &count)
	if err == nil && count > int64(limit) {
		cutoff := maxID - int64(limit)
		_, _ = ix.db.ExecContext(ctx, "DELETE FROM chunk_deletions WHERE id <= ?", cutoff)
	}
}

func (ix *Index) checkCrossProcessSync(ctx context.Context) error {
	var curVersion int64
	if err := ix.db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&curVersion); err != nil {
		return err
	}

	var maxDelID, minDelID int64
	_ = ix.db.QueryRowContext(ctx, "SELECT COALESCE(max(id), 0), COALESCE(min(id), 0) FROM chunk_deletions").Scan(&maxDelID, &minDelID)
	pruned := minDelID > 1 && ix.lastSeenDeletionID < minDelID-1

	if curVersion == ix.lastDataVersion && maxDelID <= ix.lastSeenDeletionID && !pruned {
		return nil
	}

	ix.mu.Lock()
	defer ix.mu.Unlock()

	// 1. Refresh per-context counts and pending counts
	newCounts := make(map[string]int)
	newPendingCounts := make(map[string]int)
	totalChunks := 0
	totalPending := 0
	rows, err := ix.db.QueryContext(ctx, `
		SELECT context, count(*), COALESCE(SUM(CASE WHEN vec_pending = 1 THEN 1 ELSE 0 END), 0)
		FROM chunks GROUP BY context
	`)
	if err == nil {
		for rows.Next() {
			var ctxName string
			var cnt, pCnt int
			if err := rows.Scan(&ctxName, &cnt, &pCnt); err == nil {
				newCounts[ctxName] = cnt
				newPendingCounts[ctxName] = pCnt
				totalChunks += cnt
				totalPending += pCnt
			}
		}
		rows.Close()
		ix.countsMu.Lock()
		ix.contextCounts = newCounts
		ix.pendingCounts = newPendingCounts
		ix.totalPending = totalPending
		ix.countsMu.Unlock()
	}

	// 2. Refresh vecStore if resident and ready
	if ix.residentVectors && ix.IsVecStoreReady() && ix.vecStore != nil {
		if totalChunks > ix.residentVectorCap {
			ix.capExceeded = true
			ix.vecStore = nil
			ix.setVecStoreReady(false)
		} else {
			newMaxRowid, newMaxDelID, rErr := ix.vecStore.IncrementalRefresh(ix.db, ix.maxSeenRowID, ix.lastSeenDeletionID)
			if rErr != nil {
				_ = ix.vecStore.LoadFromDB(ix.db)
				_ = ix.db.QueryRowContext(ctx, "SELECT COALESCE(max(rowid), 0) FROM chunks_vec").Scan(&ix.maxSeenRowID)
				_ = ix.db.QueryRowContext(ctx, "SELECT COALESCE(max(id), 0) FROM chunk_deletions").Scan(&ix.lastSeenDeletionID)
			} else {
				if newMaxRowid > ix.maxSeenRowID {
					ix.maxSeenRowID = newMaxRowid
				}
				if newMaxDelID > ix.lastSeenDeletionID {
					ix.lastSeenDeletionID = newMaxDelID
				}
			}
		}
	}

	ix.pruneDeletionsLocked(ctx)

	// 3. Refresh fuzzy caches
	if ix.vocabCache != nil {
		ix.vocabCache.Invalidate()
	}
	if ix.docNames != nil {
		_ = ix.docNames.LoadFromDB(ix.db)
	}

	ix.lastDataVersion = curVersion
	return nil
}

// ErrVectorsPending is returned as a non-fatal error by Upsert and UpsertMany when
// documents were successfully committed to SQLite chunks and FTS tables (so keyword search
// works immediately), but vector embeddings could not be generated and remain pending.
var ErrVectorsPending = errors.New("search: vectors pending")

// PendingVectorsError is a typed non-fatal error returned by Upsert and UpsertMany when
// vector embeddings could not be generated (e.g. embedder offline/unavailable), but
// the document write succeeded, chunks + FTS rows were committed, and keyword search works.
type PendingVectorsError struct {
	Count int
}

func (e *PendingVectorsError) Error() string {
	return fmt.Sprintf("semantic indexing pending: %d vectors pending (keyword search works)", e.Count)
}

func (e *PendingVectorsError) Is(target error) bool {
	return target == ErrVectorsPending
}

// UpsertMany inserts or replaces multiple documents with batched concurrent embedding and batched commits.
func (ix *Index) UpsertMany(ctx context.Context, docs []Doc) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.closed {
		return errors.New("index is closed")
	}
	if len(docs) == 0 {
		return nil
	}

	batchSize := ix.upsertBatchSize
	if batchSize <= 0 {
		batchSize = 16
	}
	concurrency := ix.upsertConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	type preparedChunk struct {
		chunkID      int64
		docID        string
		context      string
		chunk        Chunk
		embedText    string
		textHash     string
		embedding    []byte
		bitEmbedding []byte
	}

	type docToProcess struct {
		doc             Doc
		hash            string
		enr             Tier0Enrichment
		chunks          []Chunk
		prep            []preparedChunk
		oldChunkCount   int
		oldPendingCount int
		newPendingCount int
	}

	var toProcess []docToProcess

	df, numDocs := ix.getDFSnapshot(ctx)

	// Step 1: Check existing versions/hashes and filter unchanged docs
	for _, d := range docs {
		docHash := Sha256Hex(d.Body)
		var existingVersion int
		var existingHash string
		var pendingCount int
		err := ix.db.QueryRowContext(ctx, `
			SELECT d.version, d.hash, COALESCE(SUM(CASE WHEN c.vec_pending = 1 THEN 1 ELSE 0 END), 0)
			FROM docs d
			LEFT JOIN chunks c ON d.id = c.doc_id
			WHERE d.id = ?
			GROUP BY d.id, d.version, d.hash
		`, d.ID).Scan(&existingVersion, &existingHash, &pendingCount)
		if err == nil && existingVersion == d.Version && existingHash == docHash && pendingCount == 0 {
			continue // Unchanged doc with no pending vectors
		}

		enr, _ := ix.getOrComputeEnrichment(ctx, d.ID, d.Name, d.Body, docHash, df, numDocs)

		chunks := Chunker(d.Name, d.Body, ix.chunkOpts)
		dtp := docToProcess{
			doc:    d,
			hash:   docHash,
			enr:    enr,
			chunks: chunks,
			prep:   make([]preparedChunk, len(chunks)),
		}
		for i, c := range chunks {
			var embedText string
			if ix.disableContextualEmbed {
				embedText = c.Text
			} else {
				embedText = c.Embed()
			}
			if ix.appendKeywordsToEmbed && len(enr.Keywords) > 0 {
				embedText += "\n\nKeywords: " + strings.Join(enr.Keywords, ", ")
			}
			dtp.prep[i] = preparedChunk{
				docID:     d.ID,
				context:   d.Context,
				chunk:     c,
				embedText: embedText,
				textHash:  Sha256Hex(embedText),
			}
		}
		toProcess = append(toProcess, dtp)
	}

	if len(toProcess) == 0 {
		return nil
	}

	// Step 2: Vector reuse check
	type embedTask struct {
		docIdx   int
		chunkIdx int
		text     string
	}
	var needEmbed []embedTask

	for dIdx := range toProcess {
		dtp := &toProcess[dIdx]
		for cIdx := range dtp.prep {
			pc := &dtp.prep[cIdx]
			var reusedVec []byte
			err := ix.db.QueryRowContext(ctx, `
				SELECT v.embedding
				FROM chunks c
				JOIN chunks_vec v ON c.id = v.rowid
				WHERE c.text_hash = ?
				LIMIT 1
			`, pc.textHash).Scan(&reusedVec)
			if err == nil && len(reusedVec) == ModelDim {
				pc.embedding = reusedVec
				pc.bitEmbedding = QuantizeBitFromInt8(reusedVec)
			} else {
				needEmbed = append(needEmbed, embedTask{
					docIdx:   dIdx,
					chunkIdx: cIdx,
					text:     pc.embedText,
				})
			}
		}
	}

	// Step 3: Bounded concurrent embedding batches
	if len(needEmbed) > 0 && ix.embedder != nil {
		type batchJob struct {
			tasks []embedTask
		}
		var jobs []batchJob
		for i := 0; i < len(needEmbed); i += batchSize {
			end := i + batchSize
			if end > len(needEmbed) {
				end = len(needEmbed)
			}
			jobs = append(jobs, batchJob{tasks: needEmbed[i:end]})
		}

		jobChan := make(chan batchJob, len(jobs))
		for _, j := range jobs {
			jobChan <- j
		}
		close(jobChan)

		var wg sync.WaitGroup
		for w := 0; w < concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobChan {
					texts := make([]string, len(j.tasks))
					for idx, t := range j.tasks {
						texts[idx] = t.text
					}

					fvecs, err := ix.embedder.Embed(ctx, llm.KindDocument, texts)
					if err != nil {
						// Embedder unavailable or failed: leave embeddings as nil (will be marked vec_pending)
						continue
					}
					for idx, t := range j.tasks {
						if idx < len(fvecs) && len(fvecs[idx]) == ModelDim {
							fv := fvecs[idx]
							toProcess[t.docIdx].prep[t.chunkIdx].embedding = Quantize(fv)
							toProcess[t.docIdx].prep[t.chunkIdx].bitEmbedding = QuantizeBit(fv)
						}
					}
				}
			}()
		}
		wg.Wait()
	}

	// Step 4: Write in transactions of <= batchCommitSize chunks
	commitLimit := ix.batchCommitSize
	if commitLimit <= 0 {
		commitLimit = 5000
	}

	var allChunks []*preparedChunk
	for dIdx := range toProcess {
		dtp := &toProcess[dIdx]
		for cIdx := range dtp.prep {
			allChunks = append(allChunks, &dtp.prep[cIdx])
		}
	}

	docIdx := 0
	for docIdx < len(toProcess) {
		tx, err := ix.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin upsert_many tx: %w", err)
		}

		stmtChunk, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks(doc_id, context, ord, heading, text, text_hash, start_line, end_line, vec_pending)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING id
		`)
		if err != nil {
			tx.Rollback()
			return err
		}
		stmtVec, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks_vec(rowid, context, embedding)
			VALUES (?, ?, vec_int8(?))
		`)
		if err != nil {
			stmtChunk.Close()
			tx.Rollback()
			return err
		}
		stmtBit, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks_vec_bit(rowid, context, embedding)
			VALUES (?, ?, vec_bit(?))
		`)
		if err != nil {
			stmtChunk.Close()
			stmtVec.Close()
			tx.Rollback()
			return err
		}
		stmtFts, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks_fts(rowid, scope, heading, text, enrichment)
			VALUES (?, ?, ?, ?, ?)
		`)
		if err != nil {
			stmtChunk.Close()
			stmtVec.Close()
			stmtBit.Close()
			tx.Rollback()
			return err
		}

		txChunksCount := 0
		var txOldChunkIDs []int64

		for docIdx < len(toProcess) && (txChunksCount == 0 || txChunksCount+len(toProcess[docIdx].prep) <= commitLimit) {
			dtp := &toProcess[docIdx]

			_ = tx.QueryRowContext(ctx, "SELECT count(*) FROM chunks WHERE doc_id = ?", dtp.doc.ID).Scan(&dtp.oldChunkCount)
			_ = tx.QueryRowContext(ctx, "SELECT count(*) FROM chunks WHERE doc_id = ? AND vec_pending = 1", dtp.doc.ID).Scan(&dtp.oldPendingCount)

			rowsOld, err := tx.QueryContext(ctx, "SELECT id FROM chunks WHERE doc_id = ?", dtp.doc.ID)
			if err == nil {
				for rowsOld.Next() {
					var cid int64
					if err := rowsOld.Scan(&cid); err == nil {
						txOldChunkIDs = append(txOldChunkIDs, cid)
					}
				}
				rowsOld.Close()
			}
			_, _ = tx.ExecContext(ctx, "DELETE FROM chunks_vec_bit WHERE rowid IN (SELECT id FROM chunks WHERE doc_id = ?)", dtp.doc.ID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM chunks_vec WHERE rowid IN (SELECT id FROM chunks WHERE doc_id = ?)", dtp.doc.ID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE doc_id = ?)", dtp.doc.ID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM chunks WHERE doc_id = ?", dtp.doc.ID)
			_, _ = tx.ExecContext(ctx, "DELETE FROM docs WHERE id = ?", dtp.doc.ID)

			var tagsJSON string
			if len(dtp.doc.Tags) > 0 {
				b, _ := json.Marshal(dtp.doc.Tags)
				tagsJSON = string(b)
			} else {
				tagsJSON = "[]"
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO docs(id, context, name, version, hash, tags) VALUES (?, ?, ?, ?, ?, ?)",
				dtp.doc.ID, dtp.doc.Context, dtp.doc.Name, dtp.doc.Version, dtp.hash, tagsJSON); err != nil {
				stmtChunk.Close()
				stmtVec.Close()
				stmtBit.Close()
				stmtFts.Close()
				tx.Rollback()
				return fmt.Errorf("insert doc: %w", err)
			}

			scopeTok := ScopeToken(dtp.doc.Context)

			for i := range dtp.prep {
				pc := &dtp.prep[i]
				vecPending := 0
				if pc.embedding == nil || len(pc.embedding) != ModelDim {
					vecPending = 1
					dtp.newPendingCount++
				}

				var chunkID int64
				if err := stmtChunk.QueryRowContext(ctx,
					dtp.doc.ID, dtp.doc.Context, pc.chunk.Ord, pc.chunk.Heading, pc.chunk.Text, pc.textHash, pc.chunk.StartLine, pc.chunk.EndLine, vecPending,
				).Scan(&chunkID); err != nil {
					stmtChunk.Close()
					stmtVec.Close()
					stmtBit.Close()
					stmtFts.Close()
					tx.Rollback()
					return fmt.Errorf("insert chunk: %w", err)
				}
				pc.chunkID = chunkID

				if vecPending == 0 {
					if _, err := stmtVec.ExecContext(ctx, chunkID, dtp.doc.Context, pc.embedding); err != nil {
						stmtChunk.Close()
						stmtVec.Close()
						stmtBit.Close()
						stmtFts.Close()
						tx.Rollback()
						return fmt.Errorf("insert vec: %w", err)
					}
					if _, err := stmtBit.ExecContext(ctx, chunkID, dtp.doc.Context, pc.bitEmbedding); err != nil {
						stmtChunk.Close()
						stmtVec.Close()
						stmtBit.Close()
						stmtFts.Close()
						tx.Rollback()
						return fmt.Errorf("insert bit: %w", err)
					}
				}
				var header string
				if ix.disableContextualEmbed {
					header = pc.chunk.Heading
				} else {
					header = BuildChunkHeader(dtp.doc.Name, pc.chunk.Heading)
				}
				var enrichText string
				if !ix.disableTier0FTS {
					enrichText = strings.TrimSpace(dtp.enr.Title + "\n" + strings.Join(dtp.enr.Keywords, " ") + "\n" + dtp.enr.Lead)
				}
				if _, err := stmtFts.ExecContext(ctx, chunkID, scopeTok, header, pc.chunk.Text, enrichText); err != nil {
					stmtChunk.Close()
					stmtVec.Close()
					stmtBit.Close()
					stmtFts.Close()
					tx.Rollback()
					return fmt.Errorf("insert fts: %w", err)
				}
				txChunksCount++
				if chunkID > ix.maxSeenRowID {
					ix.maxSeenRowID = chunkID
				}
			}
			docIdx++
		}

		stmtChunk.Close()
		stmtVec.Close()
		stmtBit.Close()
		stmtFts.Close()

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit upsert_many batch: %w", err)
		}

		if ix.vecStore != nil && ix.IsVecStoreReady() {
			if len(txOldChunkIDs) > 0 {
				ix.vecStore.Delete(txOldChunkIDs)
			}
		}
	}

	ix.countsMu.Lock()
	totalNewPending := 0
	for _, dtp := range toProcess {
		ix.contextCounts[dtp.doc.Context] += len(dtp.prep) - dtp.oldChunkCount
		if ix.contextCounts[dtp.doc.Context] < 0 {
			ix.contextCounts[dtp.doc.Context] = 0
		}
		totalNewPending += dtp.newPendingCount
		pendingDelta := dtp.newPendingCount - dtp.oldPendingCount
		ix.pendingCounts[dtp.doc.Context] += pendingDelta
		if ix.pendingCounts[dtp.doc.Context] < 0 {
			ix.pendingCounts[dtp.doc.Context] = 0
		}
		ix.totalPending += pendingDelta
		if ix.totalPending < 0 {
			ix.totalPending = 0
		}
	}
	ix.countsMu.Unlock()

	if ix.vecStore != nil && ix.IsVecStoreReady() {
		for _, pc := range allChunks {
			if pc.embedding != nil && len(pc.embedding) == ModelDim {
				ix.vecStore.Upsert(pc.chunkID, pc.context, pc.embedding, pc.bitEmbedding)
			}
		}
	}

	if ix.docNames != nil {
		for _, dtp := range toProcess {
			var firstCID int64
			var headings []string
			if len(dtp.prep) > 0 {
				firstCID = dtp.prep[0].chunkID
				for _, p := range dtp.prep {
					if p.chunk.Heading != "" {
						headings = append(headings, strings.ToLower(p.chunk.Heading))
					}
				}
			}
			ix.docNames.Upsert(dtp.doc.ID, dtp.doc.Context, dtp.doc.Name, firstCID, headings, dtp.doc.Tags)
		}
	}
	if ix.vocabCache != nil {
		ix.vocabCache.Invalidate()
	}

	ix.pruneDeletionsLocked(ctx)

	if totalNewPending > 0 {
		return &PendingVectorsError{Count: totalNewPending}
	}

	return nil
}

func (ix *Index) Upsert(ctx context.Context, d Doc) error {
	return ix.UpsertMany(ctx, []Doc{d})
}

func (ix *Index) Delete(ctx context.Context, docID string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.closed {
		return errors.New("index is closed")
	}

	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var docCtx string
	var chunkCount int
	var pendingCount int
	_ = tx.QueryRowContext(ctx, "SELECT context, count(*), COALESCE(SUM(CASE WHEN vec_pending = 1 THEN 1 ELSE 0 END), 0) FROM chunks WHERE doc_id = ? GROUP BY context", docID).Scan(&docCtx, &chunkCount, &pendingCount)

	var oldChunkIDs []int64
	rowsOld, err := tx.QueryContext(ctx, "SELECT id FROM chunks WHERE doc_id = ?", docID)
	if err == nil {
		for rowsOld.Next() {
			var cid int64
			if err := rowsOld.Scan(&cid); err == nil {
				oldChunkIDs = append(oldChunkIDs, cid)
			}
		}
		rowsOld.Close()
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks_vec_bit WHERE rowid IN (SELECT id FROM chunks WHERE doc_id = ?)", docID); err != nil {
		return fmt.Errorf("delete chunks_vec_bit: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks_vec WHERE rowid IN (SELECT id FROM chunks WHERE doc_id = ?)", docID); err != nil {
		return fmt.Errorf("delete chunks_vec: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE doc_id = ?)", docID); err != nil {
		return fmt.Errorf("delete chunks_fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE doc_id = ?", docID); err != nil {
		return fmt.Errorf("delete chunks: %w", err)
	}
	_, _ = tx.ExecContext(ctx, "DELETE FROM doc_enrichments WHERE doc_id = ?", docID)
	if _, err := tx.ExecContext(ctx, "DELETE FROM docs WHERE id = ?", docID); err != nil {
		return fmt.Errorf("delete doc: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if docCtx != "" {
		ix.countsMu.Lock()
		ix.contextCounts[docCtx] -= chunkCount
		if ix.contextCounts[docCtx] < 0 {
			ix.contextCounts[docCtx] = 0
		}
		ix.pendingCounts[docCtx] -= pendingCount
		if ix.pendingCounts[docCtx] < 0 {
			ix.pendingCounts[docCtx] = 0
		}
		ix.totalPending -= pendingCount
		if ix.totalPending < 0 {
			ix.totalPending = 0
		}
		ix.countsMu.Unlock()
	}

	if len(oldChunkIDs) > 0 && ix.vecStore != nil {
		ix.vecStore.Delete(oldChunkIDs)
	}

	if ix.docNames != nil {
		ix.docNames.Delete(docID)
	}
	if ix.vocabCache != nil {
		ix.vocabCache.Invalidate()
	}

	ix.pruneDeletionsLocked(ctx)

	return nil
}

func (ix *Index) DeleteContext(ctx context.Context, slug string) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.closed {
		return errors.New("index is closed")
	}

	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks_vec_bit WHERE rowid IN (SELECT id FROM chunks WHERE context = ?)", slug); err != nil {
		return fmt.Errorf("delete chunks_vec_bit for context %s: %w", slug, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks_vec WHERE rowid IN (SELECT id FROM chunks WHERE context = ?)", slug); err != nil {
		return fmt.Errorf("delete chunks_vec for context %s: %w", slug, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks_fts WHERE rowid IN (SELECT id FROM chunks WHERE context = ?)", slug); err != nil {
		return fmt.Errorf("delete chunks_fts for context %s: %w", slug, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE context = ?", slug); err != nil {
		return fmt.Errorf("delete chunks for context %s: %w", slug, err)
	}
	_, _ = tx.ExecContext(ctx, "DELETE FROM doc_enrichments WHERE doc_id IN (SELECT id FROM docs WHERE context = ?)", slug)
	if _, err := tx.ExecContext(ctx, "DELETE FROM docs WHERE context = ?", slug); err != nil {
		return fmt.Errorf("delete docs for context %s: %w", slug, err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	ix.countsMu.Lock()
	delPending := ix.pendingCounts[slug]
	delete(ix.contextCounts, slug)
	delete(ix.pendingCounts, slug)
	ix.totalPending -= delPending
	if ix.totalPending < 0 {
		ix.totalPending = 0
	}
	ix.countsMu.Unlock()

	if ix.vecStore != nil {
		ix.vecStore.DeleteContext(slug)
	}

	if ix.docNames != nil {
		ix.docNames.DeleteContext(slug)
	}
	if ix.vocabCache != nil {
		ix.vocabCache.Invalidate()
	}

	ix.pruneDeletionsLocked(ctx)

	return nil
}

func (ix *Index) ContextChunkCount(context string) int {
	ix.countsMu.RLock()
	defer ix.countsMu.RUnlock()
	return ix.contextCounts[context]
}

func (ix *Index) TotalChunksInContexts(contexts []string) int {
	ix.countsMu.RLock()
	defer ix.countsMu.RUnlock()
	total := 0
	for _, c := range contexts {
		total += ix.contextCounts[c]
	}
	return total
}

// TotalChunks returns the total number of chunks across all contexts.
func (ix *Index) TotalChunks() int {
	ix.countsMu.RLock()
	defer ix.countsMu.RUnlock()
	total := 0
	for _, cnt := range ix.contextCounts {
		total += cnt
	}
	return total
}

// PendingVectors returns the total number of chunks pending vector embedding.
func (ix *Index) PendingVectors() int {
	ix.countsMu.RLock()
	defer ix.countsMu.RUnlock()
	return ix.totalPending
}

// PendingCount is an alias for PendingVectors.
func (ix *Index) PendingCount() int {
	return ix.PendingVectors()
}

// Pending is an alias for PendingVectors.
func (ix *Index) Pending() int {
	return ix.PendingVectors()
}

// ContextPendingVectors returns the number of chunks pending vector embedding in the specified context.
func (ix *Index) ContextPendingVectors(context string) int {
	ix.countsMu.RLock()
	defer ix.countsMu.RUnlock()
	return ix.pendingCounts[context]
}

// FillPendingVectors attempts to embed any chunks marked as pending vectors.
// Returns the number of chunks whose vectors were successfully filled.
func (ix *Index) FillPendingVectors(ctx context.Context) (int, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if ix.closed {
		return 0, errors.New("index is closed")
	}

	rows, err := ix.db.QueryContext(ctx, `
		SELECT c.id, c.doc_id, c.context, c.ord, c.heading, c.text, d.name
		FROM chunks c
		JOIN docs d ON c.doc_id = d.id
		WHERE c.vec_pending = 1
		ORDER BY c.id ASC
		LIMIT 5000
	`)
	if err != nil {
		return 0, fmt.Errorf("query pending chunks: %w", err)
	}
	defer rows.Close()

	type pendingItem struct {
		id      int64
		docID   string
		context string
		ord     int
		heading string
		text    string
		docName string
	}
	var items []pendingItem
	for rows.Next() {
		var it pendingItem
		if err := rows.Scan(&it.id, &it.docID, &it.context, &it.ord, &it.heading, &it.text, &it.docName); err == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	if len(items) == 0 {
		return 0, nil
	}

	texts := make([]string, len(items))
	for i, it := range items {
		texts[i] = (&Chunk{DocName: it.docName, Heading: it.heading, Text: it.text}).Embed()
	}

	fvecs, err := ix.embedder.Embed(ctx, llm.KindDocument, texts)
	if err != nil {
		return 0, fmt.Errorf("embed pending texts: %w", err)
	}
	if len(fvecs) != len(items) {
		return 0, fmt.Errorf("embedder returned %d vectors for %d items", len(fvecs), len(items))
	}

	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin fill pending tx: %w", err)
	}
	defer tx.Rollback()

	stmtVec, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO chunks_vec(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
	if err != nil {
		return 0, err
	}
	defer stmtVec.Close()

	stmtBit, err := tx.PrepareContext(ctx, "INSERT OR REPLACE INTO chunks_vec_bit(rowid, context, embedding) VALUES (?, ?, vec_bit(?))")
	if err != nil {
		return 0, err
	}
	defer stmtBit.Close()

	stmtUpdate, err := tx.PrepareContext(ctx, "UPDATE chunks SET vec_pending = 0 WHERE id = ?")
	if err != nil {
		return 0, err
	}
	defer stmtUpdate.Close()

	for i, it := range items {
		i8 := Quantize(fvecs[i])
		bit := QuantizeBit(fvecs[i])
		if _, err := stmtVec.ExecContext(ctx, it.id, it.context, i8); err != nil {
			return 0, fmt.Errorf("insert pending chunks_vec: %w", err)
		}
		if _, err := stmtBit.ExecContext(ctx, it.id, it.context, bit); err != nil {
			return 0, fmt.Errorf("insert pending chunks_vec_bit: %w", err)
		}
		if _, err := stmtUpdate.ExecContext(ctx, it.id); err != nil {
			return 0, fmt.Errorf("update chunks vec_pending: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit fill pending tx: %w", err)
	}

	ix.countsMu.Lock()
	for i, it := range items {
		if ix.pendingCounts[it.context] > 0 {
			ix.pendingCounts[it.context]--
		}
		if ix.totalPending > 0 {
			ix.totalPending--
		}
		if ix.vecStore != nil && ix.IsVecStoreReady() {
			i8 := Quantize(fvecs[i])
			bit := QuantizeBit(fvecs[i])
			ix.vecStore.Upsert(it.id, it.context, i8, bit)
		}
	}
	ix.countsMu.Unlock()

	return len(items), nil
}

func (ix *Index) State() (map[string]DocState, error) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if ix.closed {
		return nil, errors.New("index is closed")
	}

	rows, err := ix.db.Query(`
		SELECT d.id, d.context, d.version, d.hash,
		       COALESCE(SUM(CASE WHEN c.vec_pending = 1 THEN 1 ELSE 0 END), 0)
		FROM docs d
		LEFT JOIN chunks c ON d.id = c.doc_id
		GROUP BY d.id, d.context, d.version, d.hash
	`)
	if err != nil {
		return nil, fmt.Errorf("query docs state: %w", err)
	}
	defer rows.Close()

	state := make(map[string]DocState)
	for rows.Next() {
		var id string
		var ds DocState
		var p int
		if err := rows.Scan(&id, &ds.Context, &ds.Version, &ds.Hash, &p); err != nil {
			return nil, err
		}
		ds.PendingVectors = p
		ds.Pending = p
		state[id] = ds
	}
	return state, nil
}

func (ix *Index) getDFSnapshot(ctx context.Context) (map[string]int, int) {
	ix.dfMu.RLock()
	if len(ix.dfSnapshot) > 0 && ix.dfTotalDocs > 0 {
		df := ix.dfSnapshot
		n := ix.dfTotalDocs
		ix.dfMu.RUnlock()
		return df, n
	}
	ix.dfMu.RUnlock()

	ix.dfMu.Lock()
	defer ix.dfMu.Unlock()
	if len(ix.dfSnapshot) > 0 && ix.dfTotalDocs > 0 {
		return ix.dfSnapshot, ix.dfTotalDocs
	}

	df := make(map[string]int)
	var totalDocs int
	_ = ix.db.QueryRowContext(ctx, "SELECT count(*) FROM docs").Scan(&totalDocs)

	rows, err := ix.db.QueryContext(ctx, "SELECT term, doc FROM chunks_fts_vocab")
	if err == nil {
		for rows.Next() {
			var term string
			var doc int
			if err := rows.Scan(&term, &doc); err == nil {
				df[term] = doc
			}
		}
		rows.Close()
	}

	if totalDocs <= 0 {
		totalDocs = 1
	}
	ix.dfSnapshot = df
	ix.dfTotalDocs = totalDocs
	return df, totalDocs
}

func (ix *Index) getOrComputeEnrichment(ctx context.Context, docID, docName, docBody, docHash string, df map[string]int, numDocs int) (Tier0Enrichment, error) {
	var enr Tier0Enrichment
	var keywordsJoined string
	err := ix.db.QueryRowContext(ctx, `
		SELECT title, keywords, lead
		FROM doc_enrichments
		WHERE doc_id = ? AND hash = ?
	`, docID, docHash).Scan(&enr.Title, &keywordsJoined, &enr.Lead)

	if err == nil {
		if keywordsJoined != "" {
			enr.Keywords = strings.Split(keywordsJoined, "\t")
		}
		return enr, nil
	}

	enr = ExtractEnrichment(docName, docBody, df, numDocs)
	keywordsJoined = strings.Join(enr.Keywords, "\t")

	_, err = ix.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO doc_enrichments (doc_id, hash, title, keywords, lead)
		VALUES (?, ?, ?, ?, ?)
	`, docID, docHash, enr.Title, keywordsJoined, enr.Lead)
	return enr, err
}
