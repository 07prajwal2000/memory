package main

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"os"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func main() {
	dbFile := "test_float_ins.db"
	_ = os.Remove(dbFile)
	_ = os.Remove(dbFile + "-wal")
	_ = os.Remove(dbFile + "-shm")
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, _ = db.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;")
	_, err = db.Exec("CREATE VIRTUAL TABLE vec_f USING vec0(context text partition key, embedding float[384]);")
	if err != nil {
		log.Fatal(err)
	}

	buf := new(bytes.Buffer)
	for i := 0; i < 384; i++ {
		_ = binary.Write(buf, binary.LittleEndian, float32(0.01))
	}
	dummy := buf.Bytes()

	fmt.Println("Testing float32 insertion...")
	batchSize := 5000
	for b := 0; b < 10; b++ {
		tx, err := db.Begin()
		if err != nil {
			log.Fatalf("begin %d: %v", b, err)
		}
		stmt, err := tx.Prepare("INSERT INTO vec_f(rowid, context, embedding) VALUES (?, ?, vec_f32(?))")
		if err != nil {
			log.Fatalf("prepare: %v", err)
		}
		for i := 0; i < batchSize; i++ {
			rowid := int64(b*batchSize + i + 1)
			ctx := fmt.Sprintf("ctx%d", b)
			if _, err := stmt.Exec(rowid, ctx, dummy); err != nil {
				log.Fatalf("insert %d: %v", rowid, err)
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit %d: %v", b, err)
		}
		// checkpoint periodically
		_, _ = db.Exec("PRAGMA wal_checkpoint(PASSIVE);")
		fmt.Printf("Committed batch %d (total %d rows)\n", b+1, (b+1)*batchSize)
	}
	fmt.Println("Float32 test SUCCESS!")
}
