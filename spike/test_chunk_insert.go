package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func main() {
	dbFile := "test_insert.db"
	_ = os.Remove(dbFile)
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, _ = db.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;")
	_, err = db.Exec("CREATE VIRTUAL TABLE vec_test USING vec0(context text partition key, embedding int8[384]);")
	if err != nil {
		log.Fatal(err)
	}

	dummy := make([]byte, 384)

	// Test with batch commits of 10,000
	fmt.Println("Testing batch commits of 10k...")
	for b := 0; b < 10; b++ {
		tx, err := db.Begin()
		if err != nil {
			log.Fatalf("begin tx %d: %v", b, err)
		}
		stmt, err := tx.Prepare("INSERT INTO vec_test(rowid, context, embedding) VALUES (?, ?, vec_int8(?))")
		if err != nil {
			log.Fatalf("prepare: %v", err)
		}
		for i := 0; i < 10000; i++ {
			rowid := int64(b*10000 + i + 1)
			ctx := fmt.Sprintf("ctx%d", b)
			if _, err := stmt.Exec(rowid, ctx, dummy); err != nil {
				log.Fatalf("insert row %d (ctx=%s): %v", rowid, ctx, err)
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit tx %d: %v", b, err)
		}
		fmt.Printf("Committed batch %d (total %d rows)\n", b+1, (b+1)*10000)
	}
	fmt.Println("Batch commit test SUCCESS!")
}
