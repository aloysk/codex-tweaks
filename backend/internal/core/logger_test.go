package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestLoggerClearAndNewestPreview(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("before-clear")
	contents, err := logger.ReadPreviewNewestFirst()
	if err != nil || !strings.Contains(contents, "before-clear") {
		t.Fatalf("log missing: %q %v", contents, err)
	}
	if err := logger.Clear(); err != nil {
		t.Fatal(err)
	}
	contents, _ = logger.ReadPreviewNewestFirst()
	if contents != "" {
		t.Fatalf("log not cleared: %q", contents)
	}
	logger.Error("after-clear")
	contents, _ = logger.ReadPreviewNewestFirst()
	if !strings.Contains(contents, "after-clear") {
		t.Fatalf("log no longer writable: %q", contents)
	}
	info, err := os.Stat(logger.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
}

func TestLoggerReadPreviewNewestFirstLimitsLines(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var contents strings.Builder
	lineCount := logPreviewMaxLines + 5
	for index := range lineCount {
		fmt.Fprintf(&contents, "line-%04d\n", index)
	}
	if err := os.WriteFile(logger.Path, []byte(contents.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	preview, err := logger.ReadPreviewNewestFirst()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(preview, "\n")
	if len(lines) != logPreviewMaxLines {
		t.Fatalf("preview lines = %d, want %d", len(lines), logPreviewMaxLines)
	}
	if lines[0] != fmt.Sprintf("line-%04d", lineCount-1) {
		t.Fatalf("newest line = %q", lines[0])
	}
	if lines[len(lines)-1] != "line-0005" {
		t.Fatalf("oldest preview line = %q", lines[len(lines)-1])
	}
}

func TestLoggerReadPreviewNewestFirstLimitsBytes(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var contents strings.Builder
	line := strings.Repeat("x", 1023)
	for index := range 512 {
		fmt.Fprintf(&contents, "%03d-%s\n", index, line)
	}
	if err := os.WriteFile(logger.Path, []byte(contents.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	preview, err := logger.ReadPreviewNewestFirst()
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) > logPreviewMaxBytes {
		t.Fatalf("preview bytes = %d, want at most %d", len(preview), logPreviewMaxBytes)
	}
	if !strings.HasPrefix(preview, "511-") {
		t.Fatalf("preview does not start with newest line: %.16q", preview)
	}
	if strings.Contains(preview, "000-") {
		t.Fatal("preview still contains the oldest line")
	}
}

func TestLoggerEntryIsBoundedSingleLineAndSafeInBothSinks(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&stderr)
	t.Cleanup(func() { log.SetOutput(previous) })
	secret := "sk-" + strings.Repeat("fixture", 8)
	logger.Error(secret + "\n[ERROR] forged\r\t\x1b[2J " + strings.Repeat("你好🙂", 2_000))
	contents, err := os.ReadFile(logger.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) > logEntryMaxBytes || bytes.Count(contents, []byte{'\n'}) != 1 || !utf8.Valid(contents) {
		t.Fatalf("invalid entry: bytes=%d, lines=%d", len(contents), bytes.Count(contents, []byte{'\n'}))
	}
	for _, sink := range []string{string(contents), stderr.String()} {
		if strings.Contains(sink, secret) || strings.Contains(sink, "\x1b") || strings.Contains(sink, "\n[ERROR] forged") {
			t.Fatal("unsafe diagnostic reached a log sink")
		}
		if !strings.Contains(sink, `[REDACTED]\n[ERROR] forged\r\t\u001b`) {
			t.Fatal("diagnostic did not preserve an escaped, redacted summary")
		}
	}
}

func TestLoggerRotationRetainsOnlyTwoBoundedBackupsAndClearKeepsOtherFiles(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
	unrelated := logger.Path + ".unrelated"
	if err := os.WriteFile(unrelated, []byte("leave unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	for cycle := range 5 {
		line := []byte(fmt.Sprintf("cycle-%d\n", cycle))
		contents := bytes.Repeat(line, logFileMaxBytes/len(line))
		if err := os.WriteFile(logger.Path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		logger.Info(fmt.Sprintf("new-entry-%d", cycle))
	}
	var total int64
	for index := 0; index <= logBackupCount; index++ {
		info, err := os.Stat(logger.filePath(index))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > logFileMaxBytes {
			t.Fatalf("file %d exceeds capacity: %d", index, info.Size())
		}
		total += info.Size()
		contents, err := os.ReadFile(logger.filePath(index))
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf("cycle-%d", 5-index)
		if index == 0 {
			want = "new-entry-4"
		}
		if !bytes.Contains(contents, []byte(want)) {
			t.Fatalf("file %d does not contain newest retained generation %s", index, want)
		}
	}
	if total > int64(logFileMaxBytes*(logBackupCount+1)) {
		t.Fatalf("retention total = %d", total)
	}
	if _, err := os.Stat(logger.filePath(logBackupCount + 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected additional backup: %v", err)
	}
	if err := logger.Clear(); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= logBackupCount; index++ {
		if _, err := os.Stat(logger.filePath(index)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("backup %d survives clear: %v", index, err)
		}
	}
	contents, err := os.ReadFile(unrelated)
	if err != nil || string(contents) != "leave unchanged" {
		t.Fatalf("unrelated file changed: %q %v", contents, err)
	}
}

func TestLoggerNormalizesOversizedExistingFilesAndPermissions(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ApplicationName, "Logs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "codex-tweaks.log")
	contents := []byte(strings.Repeat("old entry\n", logFileMaxBytes/10+8_000) + "recent-entry\n")
	for index := 0; index <= logBackupCount; index++ {
		name := path
		if index > 0 {
			name = fmt.Sprintf("%s.%d", path, index)
		}
		if err := os.WriteFile(name, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(name, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logger, err := NewLogger(root)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= logBackupCount; index++ {
		name := logger.filePath(index)
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > logFileMaxBytes {
			t.Fatalf("old file remains oversized: %d", info.Size())
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			t.Fatalf("old log permissions = %o", info.Mode().Perm())
		}
		data, err := os.ReadFile(name)
		if err != nil || !bytes.HasSuffix(data, []byte("recent-entry\n")) {
			t.Fatalf("recent tail not preserved: %v", err)
		}
	}
}

type faultyLogWriter struct {
	writeError error
	closeError error
	short      bool
	closed     bool
}

func (w *faultyLogWriter) Write(value []byte) (int, error) {
	if w.writeError != nil {
		return 0, w.writeError
	}
	if w.short {
		return len(value) - 1, nil
	}
	return len(value), nil
}

func (w *faultyLogWriter) Close() error {
	w.closed = true
	return w.closeError
}

func TestLoggerPersistenceFailureIsVisibleAndClearAcknowledgesIt(t *testing.T) {
	for _, test := range []struct {
		name   string
		writer faultyLogWriter
		want   string
	}{
		{"short write", faultyLogWriter{short: true}, io.ErrShortWrite.Error()},
		{"write failure", faultyLogWriter{writeError: errors.New("write failed: token=fixture-sensitive")}, "write failed"},
		{"close failure", faultyLogWriter{closeError: errors.New("close failed")}, "close failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			logger, err := NewLogger(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&stderr)
			t.Cleanup(func() { log.SetOutput(previous) })
			logger.openAppend = func(string) (io.WriteCloser, error) { return &test.writer, nil }
			logger.Info("attempt one")
			logger.Info("attempt two")
			if !test.writer.closed {
				t.Fatal("failed writer was not closed")
			}
			preview, err := logger.ReadPreviewNewestFirst()
			if err != nil || !strings.Contains(preview, "Last log persistence failure") || !strings.Contains(preview, test.want) {
				t.Fatalf("failure not visible in preview: %q %v", preview, err)
			}
			if strings.Count(stderr.String(), "Log persistence failed:") != 1 {
				t.Fatalf("failure fallback was missing or repeated: %q", stderr.String())
			}
			if strings.Contains(stderr.String()+preview, "fixture-sensitive") {
				t.Fatal("persistence diagnostic exposed a credential")
			}
			logger.openAppend = nil
			logger.Info("recovered")
			preview, err = logger.ReadPreviewNewestFirst()
			if err != nil || !strings.Contains(preview, "recovered") || !strings.Contains(preview, test.want) {
				t.Fatalf("last failure was lost before acknowledgement: %q %v", preview, err)
			}
			if err := logger.Clear(); err != nil {
				t.Fatal(err)
			}
			preview, err = logger.ReadPreviewNewestFirst()
			if err != nil || preview != "" {
				t.Fatalf("successful clear did not acknowledge failure: %q %v", preview, err)
			}
		})
	}
}

func TestLoggerRejectsNonFileBackupWithoutRemovingItsContents(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backup := logger.filePath(1)
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(backup, "keep.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logger.Path, bytes.Repeat([]byte{'x'}, logFileMaxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	logger.Info("must not grow or rotate across a directory")
	preview, err := logger.ReadPreviewNewestFirst()
	if err != nil || !strings.Contains(preview, "not a regular file") {
		t.Fatalf("rotation failure not visible: %.200q %v", preview, err)
	}
	if err := logger.Clear(); err == nil {
		t.Fatal("clear accepted a non-file backup")
	}
	contents, err := os.ReadFile(unrelated)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("non-log content was changed: %q %v", contents, err)
	}
}

func TestLoggerRejectsLinkedLogWithoutChangingTheTarget(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, ApplicationName, "Logs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "unrelated.txt")
	if err := os.WriteFile(target, []byte("keep unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "codex-tweaks.log")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := NewLogger(root); err == nil {
		t.Fatal("linked log was accepted")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "keep unchanged" {
		t.Fatalf("linked target changed: %q %v", contents, err)
	}
}

func TestLoggerConcurrentEntriesRemainComplete(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	previous := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(previous) })
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for item := range 50 {
				logger.Info(fmt.Sprintf("worker=%d item=%d", worker, item))
			}
		})
	}
	workers.Wait()
	contents, err := os.ReadFile(logger.Path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) != 400 {
		t.Fatalf("entries = %d, want 400", len(lines))
	}
	for _, line := range lines {
		if strings.Count(line, "[INFO] worker=") != 1 {
			t.Fatalf("incomplete or interleaved entry: %q", line)
		}
	}
}

func TestLoggerNodeStderrOmitsExternalBodiesAndDrainsLongLines(t *testing.T) {
	logger, err := NewLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Repeat("private-fixture", 100_000) + "\nsecond-private-body\n"
	reader := strings.NewReader(body)
	process := &nodeRuntimeProcess{logger: logger, packageID: "sample-package"}
	process.readStderr(reader)
	if reader.Len() != 0 {
		t.Fatal("long stderr line was not fully drained")
	}
	preview, err := logger.ReadPreviewNewestFirst()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview, "private-fixture") || strings.Contains(preview, "second-private-body") {
		t.Fatal("external stderr body was persisted")
	}
	if strings.Count(preview, "body omitted") != 2 || !strings.Contains(preview, `package="sample-package"`) || !strings.Contains(preview, "bytes=1500001") {
		t.Fatalf("missing bounded stderr summary: %q", preview)
	}
}

func BenchmarkLoggerReadPreviewNewestFirstLargeFile(b *testing.B) {
	logger, err := NewLogger(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	file, err := os.OpenFile(logger.Path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	line := strings.Repeat("x", 127) + "\n"
	for range 100_000 {
		if _, err := file.WriteString(line); err != nil {
			b.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for range b.N {
		if _, err := logger.ReadPreviewNewestFirst(); err != nil {
			b.Fatal(err)
		}
	}
}
