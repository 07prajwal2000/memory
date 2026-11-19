package search

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"sort"
	"sync"
)

type knnCandidate struct {
	rowid    int64
	distance float64
}

type contextVecSlice struct {
	context string
	rowids  []int64
	bits    [][6]uint64
	i8      [][]byte
	idMap   map[int64]int
}

func newContextVecSlice(ctx string) *contextVecSlice {
	return &contextVecSlice{
		context: ctx,
		idMap:   make(map[int64]int),
	}
}

func (cs *contextVecSlice) upsert(rowid int64, b6 [6]uint64, i8Copy []byte) {
	if idx, exists := cs.idMap[rowid]; exists {
		cs.bits[idx] = b6
		cs.i8[idx] = i8Copy
		return
	}
	idx := len(cs.rowids)
	cs.rowids = append(cs.rowids, rowid)
	cs.bits = append(cs.bits, b6)
	cs.i8 = append(cs.i8, i8Copy)
	cs.idMap[rowid] = idx
}

func (cs *contextVecSlice) delete(rowids []int64) int {
	toDel := make(map[int64]bool, len(rowids))
	for _, r := range rowids {
		if _, ok := cs.idMap[r]; ok {
			toDel[r] = true
		}
	}
	if len(toDel) == 0 {
		return 0
	}
	n := len(cs.rowids)
	newR := make([]int64, 0, n-len(toDel))
	newB := make([][6]uint64, 0, n-len(toDel))
	newI := make([][]byte, 0, n-len(toDel))
	newMap := make(map[int64]int, n-len(toDel))

	removed := 0
	for i, r := range cs.rowids {
		if toDel[r] {
			removed++
			continue
		}
		newIdx := len(newR)
		newR = append(newR, r)
		newB = append(newB, cs.bits[i])
		newI = append(newI, cs.i8[i])
		newMap[r] = newIdx
	}
	cs.rowids = newR
	cs.bits = newB
	cs.i8 = newI
	cs.idMap = newMap
	return removed
}

// vecStore is an in-RAM vector matrix cache partitioned per context.
// It stores compact bit vectors ([6]uint64 = 48 bytes) and int8 vectors (384 bytes)
// per chunk, enabling native CPU POPCOUNT scanning (Stage 1) and fast int8 L2 rescoring (Stage 2)
// completely in Go, avoiding SQLite WASM table scanning overhead.
// Total RAM for 200,000 vectors is ~91 MB.
type vecStore struct {
	mu         sync.RWMutex
	byContext  map[string]*contextVecSlice
	idToCtx    map[int64]string
	totalCount int
}

func newVecStore() *vecStore {
	return &vecStore{
		byContext: make(map[string]*contextVecSlice),
		idToCtx:   make(map[int64]string),
	}
}

func byteSliceToBit6(b []byte) [6]uint64 {
	var out [6]uint64
	if len(b) >= 48 {
		for i := 0; i < 6; i++ {
			out[i] = binary.LittleEndian.Uint64(b[i*8 : (i+1)*8])
		}
	}
	return out
}

func (vs *vecStore) LoadFromDB(db *sql.DB) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	rows, err := db.Query("SELECT rowid, context, embedding FROM chunks_vec")
	if err != nil {
		return err
	}
	defer rows.Close()

	vs.byContext = make(map[string]*contextVecSlice)
	vs.idToCtx = make(map[int64]string)
	vs.totalCount = 0

	for rows.Next() {
		var rid int64
		var ctx string
		var rawI8 []byte
		if err := rows.Scan(&rid, &ctx, &rawI8); err != nil {
			return err
		}
		if len(rawI8) != ModelDim {
			continue
		}
		rawBit := QuantizeBitFromInt8(rawI8)
		b6 := byteSliceToBit6(rawBit)
		i8Copy := make([]byte, len(rawI8))
		copy(i8Copy, rawI8)

		cs, ok := vs.byContext[ctx]
		if !ok {
			cs = newContextVecSlice(ctx)
			vs.byContext[ctx] = cs
		}
		cs.upsert(rid, b6, i8Copy)
		vs.idToCtx[rid] = ctx
		vs.totalCount++
	}
	return nil
}

func (vs *vecStore) upsertLocked(rowid int64, context string, rawI8 []byte, rawBit []byte) {
	if len(rawBit) < 48 {
		rawBit = QuantizeBitFromInt8(rawI8)
	}
	b6 := byteSliceToBit6(rawBit)
	i8Copy := make([]byte, len(rawI8))
	copy(i8Copy, rawI8)

	if oldCtx, exists := vs.idToCtx[rowid]; exists && oldCtx != context {
		if oldSlice, ok := vs.byContext[oldCtx]; ok {
			rem := oldSlice.delete([]int64{rowid})
			vs.totalCount -= rem
		}
	}

	cs, ok := vs.byContext[context]
	if !ok {
		cs = newContextVecSlice(context)
		vs.byContext[context] = cs
	}
	if _, existed := cs.idMap[rowid]; !existed {
		vs.totalCount++
	}
	cs.upsert(rowid, b6, i8Copy)
	vs.idToCtx[rowid] = context
}

func (vs *vecStore) Upsert(rowid int64, context string, rawI8 []byte, rawBit []byte) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.upsertLocked(rowid, context, rawI8, rawBit)
}

func (vs *vecStore) deleteLocked(rowids []int64) {
	if len(rowids) == 0 {
		return
	}
	byCtx := make(map[string][]int64)
	for _, r := range rowids {
		if ctx, ok := vs.idToCtx[r]; ok {
			byCtx[ctx] = append(byCtx[ctx], r)
			delete(vs.idToCtx, r)
		}
	}

	for ctx, rids := range byCtx {
		if cs, ok := vs.byContext[ctx]; ok {
			removed := cs.delete(rids)
			vs.totalCount -= removed
		}
	}
}

func (vs *vecStore) Delete(rowids []int64) {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	vs.deleteLocked(rowids)
}

func (vs *vecStore) DeleteContext(ctxName string) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if cs, ok := vs.byContext[ctxName]; ok {
		for _, rid := range cs.rowids {
			delete(vs.idToCtx, rid)
		}
		vs.totalCount -= len(cs.rowids)
		delete(vs.byContext, ctxName)
	}
}

func (vs *vecStore) Count() int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	return vs.totalCount
}

func (vs *vecStore) CountInContexts(contexts []string) int {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if len(contexts) == 0 {
		return vs.totalCount
	}
	sum := 0
	for _, c := range contexts {
		if s, ok := vs.byContext[c]; ok {
			sum += len(s.rowids)
		}
	}
	return sum
}

// ErrPrunedDeletions is returned by IncrementalRefresh when the requested deletion ID has been pruned.
var ErrPrunedDeletions = errors.New("chunk deletions were pruned; full reload required")

func (vs *vecStore) IncrementalRefresh(db *sql.DB, minRowid int64, minDeletionID int64) (newMaxRowid int64, newMaxDeletionID int64, err error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	newMaxRowid = minRowid
	newMaxDeletionID = minDeletionID

	// Check if deletions were pruned behind minDeletionID
	var minAvailDelID int64
	err = db.QueryRow("SELECT COALESCE(min(id), 0) FROM chunk_deletions").Scan(&minAvailDelID)
	if err == nil && minAvailDelID > 1 && minDeletionID < minAvailDelID-1 {
		return newMaxRowid, newMaxDeletionID, ErrPrunedDeletions
	}

	// 1. Process deletions
	delRows, err := db.Query("SELECT id, chunk_id FROM chunk_deletions WHERE id > ? ORDER BY id ASC", minDeletionID)
	if err == nil {
		defer delRows.Close()
		var delIDs []int64
		for delRows.Next() {
			var dID, cID int64
			if err := delRows.Scan(&dID, &cID); err == nil {
				delIDs = append(delIDs, cID)
				if dID > newMaxDeletionID {
					newMaxDeletionID = dID
				}
			}
		}
		if len(delIDs) > 0 {
			vs.deleteLocked(delIDs)
		}
	}

	// 2. Process insertions
	addRows, err := db.Query("SELECT rowid, context, embedding FROM chunks_vec WHERE rowid > ? ORDER BY rowid ASC", minRowid)
	if err != nil {
		return newMaxRowid, newMaxDeletionID, err
	}
	defer addRows.Close()

	for addRows.Next() {
		var rid int64
		var ctx string
		var rawI8 []byte
		if err := addRows.Scan(&rid, &ctx, &rawI8); err != nil {
			return newMaxRowid, newMaxDeletionID, err
		}
		if len(rawI8) != ModelDim {
			continue
		}
		rawBit := QuantizeBitFromInt8(rawI8)
		vs.upsertLocked(rid, ctx, rawI8, rawBit)
		if rid > newMaxRowid {
			newMaxRowid = rid
		}
	}

	return newMaxRowid, newMaxDeletionID, nil
}

type bitCandidate struct {
	slice *contextVecSlice
	idx   int
	dist  int
}

func (vs *vecStore) getActiveSlicesLocked(contexts []string) []*contextVecSlice {
	if len(contexts) == 0 {
		slices := make([]*contextVecSlice, 0, len(vs.byContext))
		for _, s := range vs.byContext {
			if len(s.rowids) > 0 {
				slices = append(slices, s)
			}
		}
		return slices
	}

	slices := make([]*contextVecSlice, 0, len(contexts))
	for _, c := range contexts {
		if s, ok := vs.byContext[c]; ok && len(s.rowids) > 0 {
			slices = append(slices, s)
		}
	}
	return slices
}

func (vs *vecStore) SearchTwoStage(qVec []byte, qBit []byte, contexts []string, k int, stage1K int) []knnCandidate {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.totalCount == 0 {
		return nil
	}

	if stage1K <= 0 {
		stage1K = 200
	}
	if k <= 0 {
		k = 50
	}

	activeSlices := vs.getActiveSlicesLocked(contexts)
	if len(activeSlices) == 0 {
		return nil
	}

	if len(qBit) < 48 {
		qBit = QuantizeBitFromInt8(qVec)
	}
	qB6 := byteSliceToBit6(qBit)

	type topKCollector struct {
		cands []bitCandidate
		maxD  int
		k     int
	}

	collector := topKCollector{
		cands: make([]bitCandidate, 0, stage1K+1),
		maxD:  999999,
		k:     stage1K,
	}

	for _, s := range activeSlices {
		n := len(s.rowids)
		for i := 0; i < n; i++ {
			b := s.bits[i]
			dist := bits.OnesCount64(qB6[0]^b[0]) +
				bits.OnesCount64(qB6[1]^b[1]) +
				bits.OnesCount64(qB6[2]^b[2]) +
				bits.OnesCount64(qB6[3]^b[3]) +
				bits.OnesCount64(qB6[4]^b[4]) +
				bits.OnesCount64(qB6[5]^b[5])

			if len(collector.cands) < collector.k {
				collector.cands = append(collector.cands, bitCandidate{slice: s, idx: i, dist: dist})
				if len(collector.cands) == collector.k {
					maxD := -1
					for _, c := range collector.cands {
						if c.dist > maxD {
							maxD = c.dist
						}
					}
					collector.maxD = maxD
				}
			} else if dist < collector.maxD {
				maxIdx := 0
				maxVal := collector.cands[0].dist
				for j := 1; j < len(collector.cands); j++ {
					if collector.cands[j].dist > maxVal {
						maxVal = collector.cands[j].dist
						maxIdx = j
					}
				}
				collector.cands[maxIdx] = bitCandidate{slice: s, idx: i, dist: dist}
				newMax := -1
				for _, c := range collector.cands {
					if c.dist > newMax {
						newMax = c.dist
					}
				}
				collector.maxD = newMax
			}
		}
	}

	cands := collector.cands
	if len(cands) == 0 {
		return nil
	}

	// Stage 2: int8 L2 distance rescore on stage 1 candidates
	type rescoreCandidate struct {
		rowid    int64
		distance float64
	}
	rescore := make([]rescoreCandidate, len(cands))
	for i, c := range cands {
		targetI8 := c.slice.i8[c.idx]
		var sumSq int32
		for d := 0; d < ModelDim; d++ {
			diff := int32(int8(qVec[d])) - int32(int8(targetI8[d]))
			sumSq += diff * diff
		}
		rescore[i] = rescoreCandidate{
			rowid:    c.slice.rowids[c.idx],
			distance: math.Sqrt(float64(sumSq)) / QuantScale,
		}
	}

	sort.Slice(rescore, func(i, j int) bool {
		return rescore[i].distance < rescore[j].distance
	})

	if len(rescore) > k {
		rescore = rescore[:k]
	}

	out := make([]knnCandidate, len(rescore))
	for i, r := range rescore {
		out[i] = knnCandidate{rowid: r.rowid, distance: r.distance}
	}
	return out
}

func (vs *vecStore) SearchExact(qVec []byte, contexts []string, k int) []knnCandidate {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	if vs.totalCount == 0 {
		return nil
	}
	if k <= 0 {
		k = 50
	}

	activeSlices := vs.getActiveSlicesLocked(contexts)
	if len(activeSlices) == 0 {
		return nil
	}

	var cands []knnCandidate
	for _, s := range activeSlices {
		n := len(s.rowids)
		for i := 0; i < n; i++ {
			targetI8 := s.i8[i]
			var sumSq int32
			for d := 0; d < ModelDim; d++ {
				diff := int32(int8(qVec[d])) - int32(int8(targetI8[d]))
				sumSq += diff * diff
			}
			cands = append(cands, knnCandidate{
				rowid:    s.rowids[i],
				distance: math.Sqrt(float64(sumSq)) / QuantScale,
			})
		}
	}

	sort.Slice(cands, func(i, j int) bool {
		return cands[i].distance < cands[j].distance
	})

	if len(cands) > k {
		cands = cands[:k]
	}
	return cands
}
