package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	_ "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3/driver"
)

func runInit(dbPath string) {
	_ = os.Remove(dbPath)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("init open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;"); err != nil {
		log.Fatalf("init pragma: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS test_conc (id INTEGER PRIMARY KEY, writer TEXT, ts DATETIME);"); err != nil {
		log.Fatalf("init table: %v", err)
	}
	fmt.Println("DB initialized with WAL and busy_timeout=5000")
}

func runReader(dbPath string) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("reader open: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		log.Fatalf("reader pragma: %v", err)
	}

	start := time.Now()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM test_conc;").Scan(&count); err != nil {
		log.Fatalf("reader query: %v", err)
	}
	fmt.Printf("[Reader] Read count=%d successfully in %v\n", count, time.Since(start))
}

func runWriter(dbPath, name string, holdDuration time.Duration) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("writer %s open: %v", name, err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		log.Fatalf("writer %s pragma: %v", name, err)
	}

	start := time.Now()
	// Using sql.LevelSerializable instructs ncruces/go-sqlite3 driver to execute BEGIN IMMEDIATE
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		log.Fatalf("writer %s begin immediate: %v", name, err)
	}

	fmt.Printf("[Writer %s] Acquired BEGIN IMMEDIATE at %v. Holding for %v...\n", name, time.Since(start), holdDuration)
	time.Sleep(holdDuration)

	_, err = tx.Exec("INSERT INTO test_conc (writer, ts) VALUES (?, datetime('now'));", name)
	if err != nil {
		_ = tx.Rollback()
		log.Fatalf("writer %s insert: %v", name, err)
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("writer %s commit: %v", name, err)
	}
	fmt.Printf("[Writer %s] Committed successfully at %v (total %v)\n", name, time.Now().Format("15:04:05.000"), time.Since(start))
}

func main() {
	mode := flag.String("mode", "coord", "mode: coord, init, reader, writer")
	dbPath := flag.String("db", "test_conc.db", "db path")
	name := flag.String("name", "w1", "writer name")
	hold := flag.Duration("hold", 1500*time.Millisecond, "hold duration")
	flag.Parse()

	absDB, _ := filepath.Abs(*dbPath)

	switch *mode {
	case "init":
		runInit(absDB)
	case "reader":
		runReader(absDB)
	case "writer":
		runWriter(absDB, *name, *hold)
	case "coord":
		runCoordinator(absDB)
	default:
		log.Fatalf("unknown mode: %s", *mode)
	}
}

func runCoordinator(dbPath string) {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("executable: %v", err)
	}

	fmt.Println("--- Test A: One writer holding lock, one reader reading concurrently ---")
	runInit(dbPath)

	// Start writer 1 holding for 2 seconds
	cmdW1 := exec.Command(exe, "-mode", "writer", "-db", dbPath, "-name", "W1", "-hold", "2s")
	cmdW1.Stdout = os.Stdout
	cmdW1.Stderr = os.Stderr
	if err := cmdW1.Start(); err != nil {
		log.Fatalf("start W1: %v", err)
	}

	// Wait 500ms so W1 definitely has the transaction open
	time.Sleep(500 * time.Millisecond)

	// Start reader while W1 is still holding
	cmdR := exec.Command(exe, "-mode", "reader", "-db", dbPath)
	cmdR.Stdout = os.Stdout
	cmdR.Stderr = os.Stderr
	if err := cmdR.Run(); err != nil {
		log.Fatalf("Reader failed during write: %v", err)
	}
	fmt.Println("Test A: Reader succeeded while writer was holding transaction!")

	if err := cmdW1.Wait(); err != nil {
		log.Fatalf("W1 wait: %v", err)
	}

	fmt.Println("\n--- Test B: Two writers concurrently with BEGIN IMMEDIATE under 5s busy timeout ---")
	// W1 holds for 1.8 seconds. W2 starts after 400ms and tries to write.
	cmdW1 = exec.Command(exe, "-mode", "writer", "-db", dbPath, "-name", "W1", "-hold", "1800ms")
	cmdW1.Stdout = os.Stdout
	cmdW1.Stderr = os.Stderr
	if err := cmdW1.Start(); err != nil {
		log.Fatalf("start W1: %v", err)
	}

	time.Sleep(400 * time.Millisecond)

	cmdW2 := exec.Command(exe, "-mode", "writer", "-db", dbPath, "-name", "W2", "-hold", "200ms")
	cmdW2.Stdout = os.Stdout
	cmdW2.Stderr = os.Stderr
	if err := cmdW2.Start(); err != nil {
		log.Fatalf("start W2: %v", err)
	}

	err1 := cmdW1.Wait()
	err2 := cmdW2.Wait()

	if err1 != nil {
		log.Fatalf("W1 failed: %v", err1)
	}
	if err2 != nil {
		log.Fatalf("W2 failed: %v", err2)
	}

	fmt.Println("Test B: Both concurrent writers succeeded without 'database is locked' errors!")
}
