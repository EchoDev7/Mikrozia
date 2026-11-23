package tgbot

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/web/service"
)

type recordedDocUpload struct {
	ChatID   int64
	Filename string
	Caption  string
	Size     int64
	Header   []byte
}

func startStreamingTelegramMock(t *testing.T, failChatID int64, failAll bool) (*httptest.Server, func() []recordedDocUpload) {
	t.Helper()
	var mu sync.Mutex
	var uploads []recordedDocUpload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Content-Type", "application/json")
		if method != "sendDocument" {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 101,
					"date":       0,
					"chat":       map[string]any{"id": int64(1), "type": "private"},
				},
			})
			return
		}

		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var doc recordedDocUpload
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Errorf("NextPart: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			switch part.FormName() {
			case "chat_id":
				raw, _ := io.ReadAll(io.LimitReader(part, 64))
				doc.ChatID, _ = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
			case "caption":
				raw, _ := io.ReadAll(io.LimitReader(part, 4096))
				doc.Caption = string(raw)
			case "document":
				doc.Filename = part.FileName()
				var head [16]byte
				n, readErr := io.ReadFull(part, head[:])
				if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
					t.Errorf("read document header: %v", readErr)
				}
				doc.Header = append([]byte(nil), head[:n]...)
				rest, copyErr := io.Copy(io.Discard, part)
				if copyErr != nil {
					t.Errorf("io.Copy(io.Discard, part): %v", copyErr)
				}
				doc.Size = int64(n) + rest
			default:
				_, _ = io.Copy(io.Discard, part)
			}
		}

		if failAll || (failChatID != 0 && doc.ChatID == failChatID) {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  500,
				"description": "simulated telegram upload failure",
			})
			return
		}

		mu.Lock()
		uploads = append(uploads, doc)
		mu.Unlock()

		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 202,
				"date":       0,
				"chat":       map[string]any{"id": doc.ChatID, "type": "private"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recordedDocUpload {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedDocUpload(nil), uploads...)
	}
}

func assertNoMikroziaBackupDirs(t *testing.T, dbFolder string) {
	t.Helper()
	entries, err := os.ReadDir(dbFolder)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dbFolder, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mikrozia-backup-") {
			t.Fatalf("leftover backup temp dir: %s", entry.Name())
		}
	}
}

func TestTgbotSendBackupScreenStreamsFromReaderAndCleansUp(t *testing.T) {
	srv, uploads := startStreamingTelegramMock(t, 0, false)
	swapTestBot(t, srv.URL)
	notices.reset()

	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	var tb Tgbot
	tb.sendBackupScreen(1001)

	got := uploads()
	if len(got) == 0 {
		t.Fatal("expected sendDocument upload, got none")
	}
	dbUpload := got[0]
	if dbUpload.ChatID != 1001 {
		t.Fatalf("ChatID = %d, want 1001", dbUpload.ChatID)
	}
	if !strings.HasSuffix(dbUpload.Filename, ".db") {
		t.Fatalf("Filename = %q, want .db suffix", dbUpload.Filename)
	}
	if dbUpload.Size <= 0 {
		t.Fatalf("Size = %d, want > 0", dbUpload.Size)
	}
	if string(dbUpload.Header) != "SQLite format 3\x00" {
		t.Fatalf("uploaded header = %q, want SQLite header", string(dbUpload.Header))
	}
	if strings.TrimSpace(dbUpload.Caption) == "" {
		t.Fatal("expected non-empty caption on backup document")
	}

	assertNoMikroziaBackupDirs(t, dbFolder)
}

func TestTgbotSendBackupToAdminsMultipleAdminsReopensAndCleansUp(t *testing.T) {
	srv, uploads := startStreamingTelegramMock(t, 0, false)
	swapTestBot(t, srv.URL)
	setTestAdmins(t, 101, 202)
	notices.reset()

	tgBotMutex.Lock()
	prevRunning := isRunning
	isRunning = true
	tgBotMutex.Unlock()
	t.Cleanup(func() {
		tgBotMutex.Lock()
		isRunning = prevRunning
		tgBotMutex.Unlock()
	})

	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	var tb Tgbot
	tb.SendBackupToAdmins()

	got := uploads()
	var admin1Size, admin2Size int64
	for _, u := range got {
		if strings.HasSuffix(u.Filename, ".db") {
			switch u.ChatID {
			case 101:
				admin1Size = u.Size
			case 202:
				admin2Size = u.Size
			}
		}
	}
	if admin1Size <= 0 || admin2Size <= 0 {
		t.Fatalf("expected both admins to receive full DB backup, got admin1=%d admin2=%d", admin1Size, admin2Size)
	}
	if admin1Size != admin2Size {
		t.Fatalf("admin1 size (%d) != admin2 size (%d)", admin1Size, admin2Size)
	}

	assertNoMikroziaBackupDirs(t, dbFolder)
}

func TestTgbotSendBackupToAdminsPartialFailureStillSendsNextAndCleansUp(t *testing.T) {
	srv, uploads := startStreamingTelegramMock(t, 101, false)
	swapTestBot(t, srv.URL)
	setTestAdmins(t, 101, 202)
	notices.reset()

	tgBotMutex.Lock()
	prevRunning := isRunning
	isRunning = true
	tgBotMutex.Unlock()
	t.Cleanup(func() {
		tgBotMutex.Lock()
		isRunning = prevRunning
		tgBotMutex.Unlock()
	})

	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	var tb Tgbot
	tb.SendBackupToAdmins()

	got := uploads()
	var admin2Size int64
	for _, u := range got {
		if u.ChatID == 202 && strings.HasSuffix(u.Filename, ".db") {
			admin2Size = u.Size
		}
	}
	if admin2Size <= 0 {
		t.Fatalf("expected admin 202 to receive full backup despite admin 101 failure, got %d", admin2Size)
	}
	assertNoMikroziaBackupDirs(t, dbFolder)
}

func TestTgbotSendBackupScreenHTTPErrorCleansUp(t *testing.T) {
	srv, _ := startStreamingTelegramMock(t, 0, true)
	swapTestBot(t, srv.URL)
	notices.reset()

	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")
	if err := database.InitDB(filepath.Join(dbFolder, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	var tb Tgbot
	tb.sendBackupScreen(1001)

	assertNoMikroziaBackupDirs(t, dbFolder)
}

func TestTgbotSendDocumentReaderLarge32MiBWithoutFullHeapBuffer(t *testing.T) {
	const wantSize = int64(32 * 1024 * 1024)
	srv, uploads := startStreamingTelegramMock(t, 0, false)
	swapTestBot(t, srv.URL)
	notices.reset()

	backupDir, err := os.MkdirTemp(t.TempDir(), ".mikrozia-backup-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	backupPath := filepath.Join(backupDir, "backup.dump")
	f, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	chunk := make([]byte, 1024*1024)
	for i := 0; i < 32; i++ {
		if _, err := f.Write(chunk); err != nil {
			_ = f.Close()
			t.Fatalf("Write chunk %d: %v", i, err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	artifact := service.NewDatabaseArtifact(backupPath, "panel_test.dump", "postgres", wantSize, func() {
		_ = os.RemoveAll(backupDir)
	})
	defer artifact.Cleanup()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	var tb Tgbot
	if !tb.sendBackupArtifactWithCaption(555, artifact, "32 MiB streaming test") {
		t.Fatal("sendBackupArtifactWithCaption returned false")
	}

	runtime.ReadMemStats(&after)

	got := uploads()
	if len(got) != 1 {
		t.Fatalf("uploads = %d, want 1", len(got))
	}
	if got[0].Filename != "panel_test.dump" {
		t.Fatalf("Filename = %q, want panel_test.dump", got[0].Filename)
	}
	if got[0].Size != wantSize {
		t.Fatalf("uploaded Size = %d, want %d", got[0].Size, wantSize)
	}
	if got[0].Caption != "32 MiB streaming test" {
		t.Fatalf("Caption = %q, want %q", got[0].Caption, "32 MiB streaming test")
	}

	if after.TotalAlloc > before.TotalAlloc && after.TotalAlloc-before.TotalAlloc > 16*1024*1024 {
		t.Fatalf("TotalAlloc grew by %d bytes during 32 MiB Telegram upload; expected streaming without full RAM buffer", after.TotalAlloc-before.TotalAlloc)
	}

	artifact.Cleanup()
	if _, statErr := os.Stat(backupDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected backupDir %q removed after Cleanup, got %v", backupDir, statErr)
	}
}
