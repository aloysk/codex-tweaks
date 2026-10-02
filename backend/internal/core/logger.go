package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	logPreviewMaxLines = 1_000
	logPreviewMaxBytes = 256 * 1024
	logFileMaxBytes    = 2 * 1024 * 1024
	logBackupCount     = 2 // The current file and two backups retain at most 6 MiB.
	logEntryMaxBytes   = 4 * 1024
)

type Logger struct {
	Path string
	mu   sync.Mutex

	lastPersistenceError string
	openAppend           func(string) (io.WriteCloser, error)
}

func NewLogger(applicationSupport string) (*Logger, error) {
	if applicationSupport == "" {
		var err error
		applicationSupport, err = os.UserConfigDir()
		if err != nil {
			return nil, err
		}
	}
	directory := filepath.Join(applicationSupport, ApplicationName, "Logs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	logger := &Logger{Path: filepath.Join(directory, "codex-tweaks.log")}
	if err := logger.EnsureExists(); err != nil {
		return nil, err
	}
	return logger, nil
}

func (l *Logger) Info(message string)  { l.append("INFO", message) }
func (l *Logger) Warn(message string)  { l.append("WARN", message) }
func (l *Logger) Error(message string) { l.append("ERROR", message) }

func (l *Logger) EnsureExists() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for index := 0; index <= logBackupCount; index++ {
		if err := prepareLogFile(l.filePath(index), index == 0); err != nil {
			l.rememberPersistenceErrorLocked(err)
			return err
		}
	}
	return nil
}

func (l *Logger) ReadPreviewNewestFirst() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureExistsLocked(); err != nil {
		l.rememberPersistenceErrorLocked(err)
		return "", err
	}
	file, err := os.Open(l.Path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	readSize := info.Size()
	if readSize > logPreviewMaxBytes {
		readSize = logPreviewMaxBytes
	}
	if readSize == 0 {
		return l.previewLocked(""), nil
	}
	start := info.Size() - readSize
	contents := make([]byte, int(readSize))
	read, err := file.ReadAt(contents, start)
	if err != nil && err != io.EOF {
		return "", err
	}
	contents = contents[:read]
	if start > 0 {
		if newline := bytes.IndexByte(contents, '\n'); newline >= 0 {
			contents = contents[newline+1:]
		}
	}
	if len(contents) == 0 {
		return l.previewLocked(""), nil
	}
	contents = bytes.TrimSuffix(contents, []byte{'\n'})
	lines := strings.Split(strings.ToValidUTF8(string(contents), "�"), "\n")
	if len(lines) > logPreviewMaxLines {
		lines = lines[len(lines)-logPreviewMaxLines:]
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return l.previewLocked(strings.Join(lines, "\n")), nil
}

func (l *Logger) previewLocked(contents string) string {
	if l.lastPersistenceError != "" {
		warning := "[WARN] Last log persistence failure; records may be incomplete: " + l.lastPersistenceError
		contents = warning + "\n" + contents
	}
	lines := strings.Split(strings.TrimSuffix(contents, "\n"), "\n")
	if len(lines) > logPreviewMaxLines {
		lines = lines[:logPreviewMaxLines]
	}
	return truncateDiagnosticText(strings.Join(lines, "\n"), logPreviewMaxBytes)
}

func (l *Logger) Clear() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureExistsLocked(); err != nil {
		l.rememberPersistenceErrorLocked(err)
		return err
	}
	// Only these exact owned names participate in retention or clearing. Refuse
	// links and directories before removing anything, including the current file.
	for index := 1; index <= logBackupCount; index++ {
		if _, err := regularLogFile(l.filePath(index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.rememberPersistenceErrorLocked(err)
			return err
		}
	}
	for index := 1; index <= logBackupCount; index++ {
		if err := os.Remove(l.filePath(index)); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.rememberPersistenceErrorLocked(err)
			return err
		}
	}
	if err := os.WriteFile(l.Path, nil, 0o600); err != nil {
		l.rememberPersistenceErrorLocked(err)
		return err
	}
	l.lastPersistenceError = ""
	return nil
}

func (l *Logger) append(level, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prefix := time.Now().UTC().Format(time.RFC3339Nano) + " [" + level + "] "
	entry := []byte(prefix + safeDiagnosticText(message, logEntryMaxBytes-len(prefix)-1) + "\n")
	log.Print(strings.TrimSuffix(string(entry), "\n"))
	if err := l.appendLocked(entry); err != nil {
		l.rememberPersistenceErrorLocked(err)
	}
}

func (l *Logger) appendLocked(entry []byte) error {
	if err := l.ensureExistsLocked(); err != nil {
		return err
	}
	info, err := os.Stat(l.Path)
	if err != nil {
		return err
	}
	if info.Size()+int64(len(entry)) > logFileMaxBytes {
		if err := l.rotateLocked(); err != nil {
			return err
		}
	}
	open := l.openAppend
	if open == nil {
		open = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		}
	}
	file, err := open(l.Path)
	if err != nil {
		return err
	}
	return writeLogEntry(file, entry)
}

func writeLogEntry(file io.WriteCloser, entry []byte) error {
	written, err := file.Write(entry)
	if err == nil && written != len(entry) {
		err = io.ErrShortWrite
	}
	return errors.Join(err, file.Close())
}

func (l *Logger) rememberPersistenceErrorLocked(err error) {
	message := SafeDiagnosticText(err.Error())
	if message != l.lastPersistenceError {
		// This goes directly to stderr, never back through the failing file sink.
		log.Printf("[ERROR] Log persistence failed: %s", message)
	}
	l.lastPersistenceError = message
}

func (l *Logger) filePath(index int) string {
	if index == 0 {
		return l.Path
	}
	return fmt.Sprintf("%s.%d", l.Path, index)
}

func (l *Logger) rotateLocked() error {
	for index := 1; index <= logBackupCount; index++ {
		if err := prepareLogFile(l.filePath(index), false); err != nil {
			return err
		}
	}
	for index := logBackupCount; index > 0; index-- {
		source := l.filePath(index - 1)
		if _, err := regularLogFile(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := replaceFile(source, l.filePath(index)); err != nil {
			return err
		}
	}
	return l.ensureExistsLocked()
}

func (l *Logger) ensureExistsLocked() error {
	return prepareLogFile(l.Path, true)
}

func regularLogFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("log path is not a regular file: %s", path)
	}
	return info, err
}

func prepareLogFile(path string, create bool) error {
	info, err := regularLogFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) && !create {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if info == nil || info.Size() <= logFileMaxBytes {
		return nil
	}
	// Older versions could leave arbitrarily large files. Keep a bounded recent
	// tail before rotation; otherwise a newly rotated backup would remain huge.
	reader, err := os.Open(path)
	if err != nil {
		return err
	}
	contents := make([]byte, logFileMaxBytes)
	read, readErr := reader.ReadAt(contents, info.Size()-logFileMaxBytes)
	closeErr := reader.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(readErr, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	contents = contents[:read]
	if newline := bytes.IndexByte(contents, '\n'); newline >= 0 {
		contents = contents[newline+1:]
	}
	return writeFileAtomic(path, contents, 0o600)
}
