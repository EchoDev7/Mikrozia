package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/web/service"
)

func TestSendMessageWithFilesStreamsReaderAndRejectsInvalidAttachments(t *testing.T) {
	settingService := setupTestDB(t)
	_ = settingService.SetDiscordBotToken("test-bot-token")
	_ = settingService.SetDiscordChannelId("ch-stream")

	svc := NewDiscordService(settingService)

	invalidCases := []struct {
		name string
		file FileAttachment
	}{
		{name: "empty filename", file: FileAttachment{Filename: "", Data: []byte("x")}},
		{name: "nil source", file: FileAttachment{Filename: "a.db"}},
		{name: "both data and reader", file: FileAttachment{Filename: "a.db", Data: []byte("x"), Reader: strings.NewReader("y")}},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.SendMessageWithFiles(context.Background(), MessagePayload{Content: "hi"}, tc.file); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}

	var gotPayload MessagePayload
	gotFiles := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader: %v", err)
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("NextPart: %v", err)
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "payload_json" {
				_ = json.Unmarshal(data, &gotPayload)
			} else {
				gotFiles[part.FileName()] = string(data)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer server.Close()

	svc.SetBaseURL(server.URL)
	svc.SetHTTPClient(server.Client())

	err := svc.SendMessageWithFiles(
		context.Background(),
		MessagePayload{Content: "stream-check"},
		FileAttachment{Filename: "panel.dump", Reader: strings.NewReader("streamed-dump-bytes")},
		FileAttachment{Filename: "config.json", Data: []byte(`{"ok":true}`)},
	)
	if err != nil {
		t.Fatalf("SendMessageWithFiles: %v", err)
	}
	if gotPayload.Content != "stream-check" {
		t.Fatalf("payload.Content = %q, want stream-check", gotPayload.Content)
	}
	if gotFiles["panel.dump"] != "streamed-dump-bytes" {
		t.Fatalf("panel.dump = %q, want streamed-dump-bytes", gotFiles["panel.dump"])
	}
	if gotFiles["config.json"] != `{"ok":true}` {
		t.Fatalf("config.json = %q, want {\"ok\":true}", gotFiles["config.json"])
	}
}

func TestSendMessageWithFilesLarge64MiBWithoutFullHeapBuffer(t *testing.T) {
	const wantSize = int64(64 * 1024 * 1024)
	settingService := setupTestDB(t)
	_ = settingService.SetDiscordBotToken("test-bot-token")
	_ = settingService.SetDiscordChannelId("ch-64m")

	tmpPath := filepath.Join(t.TempDir(), "large.dump")
	wf, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	chunk := make([]byte, 1024*1024)
	for i := 0; i < 64; i++ {
		if _, err := wf.Write(chunk); err != nil {
			_ = wf.Close()
			t.Fatalf("Write chunk %d: %v", i, err)
		}
	}
	if err := wf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var receivedFilename string
	var receivedSize int64
	var receivedPayload MessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
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
			if part.FormName() == "payload_json" {
				raw, _ := io.ReadAll(io.LimitReader(part, 8192))
				_ = json.Unmarshal(raw, &receivedPayload)
				continue
			}
			receivedFilename = part.FileName()
			n, copyErr := io.Copy(io.Discard, part)
			if copyErr != nil {
				t.Errorf("io.Copy(io.Discard, part): %v", copyErr)
			}
			receivedSize = n
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"ok-64m"}`))
	}))
	defer server.Close()

	svc := NewDiscordService(settingService)
	svc.SetBaseURL(server.URL)
	svc.SetHTTPClient(server.Client())

	rf, err := os.Open(tmpPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rf.Close()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	err = svc.SendMessageWithFiles(
		context.Background(),
		MessagePayload{Content: "64m-backup"},
		FileAttachment{Filename: "large.dump", Reader: rf},
	)
	if err != nil {
		t.Fatalf("SendMessageWithFiles(64MiB): %v", err)
	}

	runtime.ReadMemStats(&after)

	if receivedPayload.Content != "64m-backup" {
		t.Fatalf("payload.Content = %q, want 64m-backup", receivedPayload.Content)
	}
	if receivedFilename != "large.dump" {
		t.Fatalf("receivedFilename = %q, want large.dump", receivedFilename)
	}
	if receivedSize != wantSize {
		t.Fatalf("receivedSize = %d, want %d", receivedSize, wantSize)
	}
	if after.TotalAlloc > before.TotalAlloc && after.TotalAlloc-before.TotalAlloc > 24*1024*1024 {
		t.Fatalf("TotalAlloc grew by %d bytes during 64 MiB Discord upload; expected streaming without full RAM buffer", after.TotalAlloc-before.TotalAlloc)
	}
}

type failingReader struct {
	remaining int
	err       error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, f.err
	}
	n := len(p)
	if n > f.remaining {
		n = f.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'A'
	}
	f.remaining -= n
	return n, nil
}

type blockingReader struct {
	ctx context.Context
}

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func TestSendMessageWithFilesCancelReaderErrorAndEarly413(t *testing.T) {
	settingService := setupTestDB(t)
	_ = settingService.SetDiscordBotToken("test-bot-token")
	_ = settingService.SetDiscordChannelId("ch-edge")

	t.Run("reader error propagates", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		svc := NewDiscordService(settingService)
		svc.SetBaseURL(server.URL)
		svc.SetHTTPClient(server.Client())

		diskErr := errors.New("disk read boom")
		err := svc.SendMessageWithFiles(
			context.Background(),
			MessagePayload{Content: "fail-reader"},
			FileAttachment{Filename: "broken.dump", Reader: &failingReader{remaining: 1024, err: diskErr}},
		)
		if err == nil || !strings.Contains(err.Error(), "disk read boom") {
			t.Fatalf("expected reader error to propagate, got: %v", err)
		}
	})

	t.Run("context cancel stops pipe and writer without leak", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		svc := NewDiscordService(settingService)
		svc.SetBaseURL(server.URL)
		svc.SetHTTPClient(server.Client())

		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()

		err := svc.SendMessageWithFiles(
			ctx,
			MessagePayload{Content: "cancel"},
			FileAttachment{Filename: "slow.dump", Reader: &blockingReader{ctx: ctx}},
		)
		if err == nil {
			t.Fatal("expected context cancel error, got nil")
		}
	})

	t.Run("early HTTP 413 does not deadlock writer goroutine", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Immediately reject 413 without draining request body.
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"message":"Request entity too large"}`))
		}))
		defer server.Close()

		svc := NewDiscordService(settingService)
		svc.SetBaseURL(server.URL)
		svc.SetHTTPClient(server.Client())

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Stream 8 MiB of zeros; server rejects immediately with 413.
		zeroFile := io.LimitReader(zeroReader{}, 8*1024*1024)
		err := svc.SendMessageWithFiles(
			ctx,
			MessagePayload{Content: "too-big"},
			FileAttachment{Filename: "huge.dump", Reader: zeroFile},
		)
		if err == nil || !strings.Contains(err.Error(), "413") {
			t.Fatalf("expected HTTP 413 error, got: %v", err)
		}
	})
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestGatewaySendBackupStreamsFileAndCleansUp(t *testing.T) {
	dbFolder := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbFolder)
	t.Setenv("XUI_DB_TYPE", "sqlite")

	settingService := setupTestDB(t)
	_ = settingService.SetDiscordBotToken("test-bot-token")
	_ = settingService.SetDiscordChannelId("ch-gateway")

	var receivedDBFilename string
	var receivedDBSize int64
	var receivedEmbed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("MultipartReader: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
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
			if part.FormName() == "payload_json" {
				raw, _ := io.ReadAll(io.LimitReader(part, 8192))
				var p MessagePayload
				_ = json.Unmarshal(raw, &p)
				receivedEmbed = len(p.Embeds) > 0
				continue
			}
			if strings.HasSuffix(part.FileName(), ".db") {
				receivedDBFilename = part.FileName()
				receivedDBSize, _ = io.Copy(io.Discard, part)
			} else {
				_, _ = io.Copy(io.Discard, part)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"gw-backup"}`))
	}))
	defer server.Close()

	svc := NewDiscordService(settingService)
	svc.SetBaseURL(server.URL)
	svc.SetHTTPClient(server.Client())

	gw := NewGatewayClient(svc, settingService, &service.ServerService{}, nil, nil)
	gw.sendBackup(context.Background())

	if !receivedEmbed {
		t.Fatalf("expected backup embed in payload_json (dbFilename=%q, dbSize=%d)", receivedDBFilename, receivedDBSize)
	}
	if !strings.HasSuffix(receivedDBFilename, ".db") {
		t.Fatalf("receivedDBFilename = %q, want .db suffix", receivedDBFilename)
	}
	if receivedDBSize <= 0 {
		t.Fatalf("receivedDBSize = %d, want > 0", receivedDBSize)
	}

	entries, err := os.ReadDir(dbFolder)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".mikrozia-backup-") {
			t.Fatalf("leftover backup temp dir after gateway sendBackup: %s", entry.Name())
		}
	}
}
