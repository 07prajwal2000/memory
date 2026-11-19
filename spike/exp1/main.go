package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math/rand"
	"os"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func randFloatVec(dim int) []float32 {
	v := make([]float32, dim)
	var sumSq float32
	for i := range v {
		v[i] = rand.Float32()*2 - 1
		sumSq += v[i] * v[i]
	}
	norm := float32(1.0)
	if sumSq > 0 {
		norm = 1.0 / float32(len(v)) // simple scaling
	}
	for i := range v {
		v[i] *= norm
	}
	return v
}

func serializeFloat32(v []float32) []byte {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, v)
	return buf.Bytes()
}

func quantizeInt8(v []float32) []byte {
	b := make([]byte, len(v))
	for i, f := range v {
		// scale by 127, clamp -128..127
		val := int(f * 127)
		if val > 127 {
			val = 127
		} else if val < -128 {
			val = -128
		}
		b[i] = byte(int8(val))
	}
	return b
}

func quantizeBit(v []float32) []byte {
	numBytes := (len(v) + 7) / 8
	b := make([]byte, numBytes)
	for i, f := range v {
		if f > 0 {
			b[i/8] |= 1 << (i % 8)
		}
	}
	return b
}

func main() {
	dbFile := "test_exp1.db"
	_ = os.Remove(dbFile)
	_ = os.Remove(dbFile + "-wal")
	_ = os.Remove(dbFile + "-shm")
	defer func() {
		_ = os.Remove(dbFile)
		_ = os.Remove(dbFile + "-wal")
		_ = os.Remove(dbFile + "-shm")
	}()

	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// 1. Enable WAL & busy_timeout
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode = WAL;").Scan(&journalMode); err != nil {
		log.Fatalf("set WAL: %v", err)
	}
	fmt.Printf("Journal mode: %s\n", journalMode)

	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		log.Fatalf("set busy_timeout: %v", err)
	}
	fmt.Println("busy_timeout set to 5000ms")

	// 2. FTS5 table
	_, err = db.Exec("CREATE VIRTUAL TABLE fts_docs USING fts5(context UNINDEXED, content);")
	if err != nil {
		log.Fatalf("create fts5: %v", err)
	}
	_, err = db.Exec("INSERT INTO fts_docs(context, content) VALUES ('global', 'hello world test document');")
	if err != nil {
		log.Fatalf("insert fts5: %v", err)
	}
	var matchCount int
	err = db.QueryRow("SELECT count(*) FROM fts_docs WHERE fts_docs MATCH 'hello'").Scan(&matchCount)
	if err != nil {
		log.Fatalf("query fts5: %v", err)
	}
	fmt.Printf("FTS5 match count: %d\n", matchCount)

	// 3. vec0 table with partition key context and int8[384]
	_, err = db.Exec("CREATE VIRTUAL TABLE vec_int8 USING vec0(context text partition key, embedding int8[384]);")
	if err != nil {
		log.Fatalf("create vec_int8: %v", err)
	}
	fmt.Println("vec0 int8[384] created with partition key 'context'!")

	// 4. vec0 table with partition key context and float[384]
	_, err = db.Exec("CREATE VIRTUAL TABLE vec_float USING vec0(context text partition key, embedding float[384]);")
	if err != nil {
		log.Fatalf("create vec_float: %v", err)
	}
	fmt.Println("vec0 float[384] created with partition key 'context'!")

	// 5. vec0 table with partition key context and bit[384]
	_, err = db.Exec("CREATE VIRTUAL TABLE vec_bit USING vec0(context text partition key, embedding bit[384]);")
	if err != nil {
		log.Fatalf("create vec_bit: %v", err)
	}
	fmt.Println("vec0 bit[384] created with partition key 'context'!")

	// Test insert and KNN query for int8
	v1 := randFloatVec(384)
	v1Int8 := quantizeInt8(v1)
	v2 := randFloatVec(384)
	v2Int8 := quantizeInt8(v2)

	_, err = db.Exec("INSERT INTO vec_int8(rowid, context, embedding) VALUES (1, 'ctx1', vec_int8(?)), (2, 'ctx2', vec_int8(?))", v1Int8, v2Int8)
	if err != nil {
		log.Fatalf("insert vec_int8: %v", err)
	}

	// KNN query on int8 with partition filter
	rows, err := db.Query("SELECT rowid, distance FROM vec_int8 WHERE embedding MATCH vec_int8(?) AND k = 10 AND context = 'ctx1'", v1Int8)
	if err != nil {
		log.Fatalf("knn query vec_int8: %v", err)
	}
	defer rows.Close()
	var knnRows int
	for rows.Next() {
		var rID int64
		var dist float64
		if err := rows.Scan(&rID, &dist); err != nil {
			log.Fatalf("scan row: %v", err)
		}
		knnRows++
		fmt.Printf("vec_int8 result: rowid=%d, dist=%.4f\n", rID, dist)
	}
	fmt.Printf("vec_int8 KNN rows found: %d\n", knnRows)

	// Test insert and KNN query for float
	v1Float := serializeFloat32(v1)
	v2Float := serializeFloat32(v2)
	_, err = db.Exec("INSERT INTO vec_float(rowid, context, embedding) VALUES (1, 'ctx1', vec_f32(?)), (2, 'ctx2', vec_f32(?))", v1Float, v2Float)
	if err != nil {
		log.Fatalf("insert vec_float: %v", err)
	}
	rowsF, err := db.Query("SELECT rowid, distance FROM vec_float WHERE embedding MATCH vec_f32(?) AND k = 10 AND context = 'ctx1'", v1Float)
	if err != nil {
		log.Fatalf("knn query vec_float: %v", err)
	}
	defer rowsF.Close()
	for rowsF.Next() {
		var rID int64
		var dist float64
		_ = rowsF.Scan(&rID, &dist)
		fmt.Printf("vec_float result: rowid=%d, dist=%.4f\n", rID, dist)
	}

	// Test insert and KNN query for bit
	v1Bit := quantizeBit(v1)
	v2Bit := quantizeBit(v2)
	_, err = db.Exec("INSERT INTO vec_bit(rowid, context, embedding) VALUES (1, 'ctx1', vec_bit(?)), (2, 'ctx2', vec_bit(?))", v1Bit, v2Bit)
	if err != nil {
		log.Fatalf("insert vec_bit: %v", err)
	}
	rowsB, err := db.Query("SELECT rowid, distance FROM vec_bit WHERE embedding MATCH vec_bit(?) AND k = 10 AND context = 'ctx1'", v1Bit)
	if err != nil {
		log.Fatalf("knn query vec_bit: %v", err)
	}
	defer rowsB.Close()
	for rowsB.Next() {
		var rID int64
		var dist float64
		_ = rowsB.Scan(&rID, &dist)
		fmt.Printf("vec_bit result: rowid=%d, dist=%.4f\n", rID, dist)
	}

	fmt.Println("Experiment 1 part 2 ALL PASSED!")
}
