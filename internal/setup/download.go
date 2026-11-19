package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Downloader fetches files with resume support, SHA-256 verification, atomic
// rename and bounded retries. The zero value is usable via DefaultDownloader.
type Downloader struct {
	// Client is the HTTP client; nil means a client with DownloadTimeout.
	Client *http.Client
	// UserAgent is sent on every request.
	UserAgent string
	// Attempts is the total number of tries per file (default 3).
	Attempts int
	// Backoff is the delay before retry N (multiplied by N).
	Backoff time.Duration
	// Progress, when set, receives periodic progress reports.
	Progress func(Progress)
	// Log receives one line per attempt, when set.
	Log func(string)
	// DisableResume turns off Range requests (used by some tests).
	DisableResume bool
}

// DownloadTimeout bounds one HTTP request.
const DownloadTimeout = 30 * time.Minute

// Progress reports download state for a human progress line.
type Progress struct {
	File     string
	Done     int64
	Total    int64
	BytesPer time.Duration
	Speed    float64 // bytes per second, 0 when unknown
	Resumed  bool
}

// DefaultDownloader returns a downloader with production defaults.
func DefaultDownloader() *Downloader {
	return &Downloader{
		Client:    &http.Client{Timeout: DownloadTimeout},
		UserAgent: "mem/" + versionString(),
		Attempts:  3,
		Backoff:   500 * time.Millisecond,
	}
}

// versionString is set by the CLI so the User-Agent matches the binary. A
// package-level variable keeps setup independent of the cli package.
var versionString = func() string { return "0.1.0" }

// SetVersion wires the binary version into the User-Agent.
func SetVersion(v string) {
	if v != "" {
		versionString = func() string { return v }
	}
}

// ErrHashMismatch reports a finished download whose content is not the pinned
// file.
var ErrHashMismatch = errors.New("sha256 mismatch")

// Fetch downloads a URL to dest, verifying size and SHA-256.
//
// A partial download is kept in dest+".part" and resumed with a Range request;
// the finished file is verified and renamed into place atomically. A hash
// mismatch deletes the .part file so a retry starts clean.
func (d *Downloader) Fetch(url, dest string, size int64, wantSHA string) error {
	attempts := d.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	backoff := d.Backoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(backoff * time.Duration(attempt-1))
			d.logf("retry %d/%d for %s after: %v", attempt, attempts, filepath.Base(dest), lastErr)
		}
		err := d.attempt(url, dest, size, wantSHA)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrHashMismatch) {
			// A mismatch will not fix itself by resuming.
			_ = os.Remove(dest + ".part")
		}
		lastErr = err
	}
	return fmt.Errorf("download %s failed after %d attempts: %w", filepath.Base(dest), attempts, lastErr)
}

func (d *Downloader) attempt(url, dest string, size int64, wantSHA string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	part := dest + ".part"

	// Already complete?
	if ok, err := verifyFile(dest, size, wantSHA); err == nil && ok {
		d.logf("%s already present and valid", filepath.Base(dest))
		return nil
	}

	var offset int64
	if !d.DisableResume {
		if info, err := os.Stat(part); err == nil {
			offset = info.Size()
		}
	}
	if size > 0 && offset > size {
		// A stale or corrupt .part larger than the file: start over.
		_ = os.Remove(part)
		offset = 0
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", d.userAgent())
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: DownloadTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored the range (or we asked for the whole file).
		offset = 0
	case http.StatusPartialContent:
		if _, err := resp.Body.Read(nil); err != nil {
			return fmt.Errorf("read body: %w", err)
		}
	default:
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(resp.Status))
	}

	total := size
	if total <= 0 && resp.ContentLength > 0 {
		total = offset + resp.ContentLength
	}

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open part file: %w", err)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return fmt.Errorf("seek part file: %w", err)
		}
	} else if err := f.Truncate(0); err != nil {
		f.Close()
		return fmt.Errorf("truncate part file: %w", err)
	}

	done := offset
	start := time.Now()
	startDone := done
	lastReport := time.Now()

	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return fmt.Errorf("write part file: %w", err)
			}
			done += int64(n)
			if d.Progress != nil && time.Since(lastReport) > 200*time.Millisecond {
				lastReport = time.Now()
				elapsed := time.Since(start).Seconds()
				speed := 0.0
				if elapsed > 0 {
					speed = float64(done-startDone) / elapsed
				}
				d.Progress(Progress{File: filepath.Base(dest), Done: done, Total: total, Speed: speed, Resumed: offset > 0})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return fmt.Errorf("read body: %w", readErr)
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync part file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close part file: %w", err)
	}
	if d.Progress != nil {
		elapsed := time.Since(start).Seconds()
		speed := 0.0
		if elapsed > 0 {
			speed = float64(done-startDone) / elapsed
		}
		d.Progress(Progress{File: filepath.Base(dest), Done: done, Total: total, Speed: speed, Resumed: offset > 0})
	}

	if size > 0 && done != size {
		_ = os.Remove(part)
		return fmt.Errorf("size mismatch: got %d bytes, want %d", done, size)
	}
	if wantSHA != "" {
		got, err := fileSHA256(part)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, wantSHA) {
			return fmt.Errorf("%w: %s has sha256 %s, want %s", ErrHashMismatch, filepath.Base(dest), got, wantSHA)
		}
	}
	if err := os.Rename(part, dest); err != nil {
		return fmt.Errorf("install %s: %w", filepath.Base(dest), err)
	}
	return nil
}

// verifyFile reports whether path already has the expected size and hash.
func verifyFile(path string, size int64, wantSHA string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if size > 0 && info.Size() != size {
		return false, nil
	}
	if wantSHA == "" {
		return true, nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(got, wantSHA), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (d *Downloader) userAgent() string {
	if d.UserAgent != "" {
		return d.UserAgent
	}
	return "mem/" + versionString()
}

func (d *Downloader) logf(format string, args ...any) {
	if d.Log != nil {
		d.Log(fmt.Sprintf(format, args...))
	}
}
