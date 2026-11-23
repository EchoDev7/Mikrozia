package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/database"
	"github.com/echodev7/mikrozia/v3/internal/web/global"
	"github.com/echodev7/mikrozia/v3/internal/web/service"
	"github.com/echodev7/mikrozia/v3/internal/xray"

	"github.com/gin-gonic/gin"
)

func assertNoControllerImportStagingDirs(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mikrozia-import-") {
			t.Fatalf("leftover import staging dir: %s", e.Name())
		}
	}
}

func newImportTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	ctrl := &ServerController{}
	engine.POST("/panel/api/server/importDB", ctrl.importDB)
	return engine
}

func TestImportDBInvalidMaxMiBConfigReturns500(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("XUI_DB_IMPORT_MAX_MIB", "16")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("db", "x-ui.db")
	_, _ = part.Write([]byte("SQLite format 3\x00test"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	newImportTestEngine().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body=%s)", w.Code, w.Body.String())
	}
	var env hostEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if env.Success {
		t.Fatal("expected success=false")
	}
	assertNoControllerImportStagingDirs(t, dbDir)
}

func TestImportDBOversizedReturns413WithJSONEnvelope(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	t.Setenv("XUI_DB_IMPORT_MAX_MIB", "32")

	srv := httptest.NewServer(newImportTestEngine())
	defer srv.Close()

	t.Run("large Content-Length rejected with 413", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", http.NoBody)
		req.ContentLength = 40 * 1024 * 1024
		req.Header.Set("Content-Type", "multipart/form-data; boundary=abc")
		w := httptest.NewRecorder()
		newImportTestEngine().ServeHTTP(w, req)

		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body=%s)", w.Code, w.Body.String())
		}
		var env hostEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if env.Success || !strings.Contains(env.Msg, "32 MiB") {
			t.Fatalf("unexpected 413 envelope: %+v", env)
		}
		assertNoControllerImportStagingDirs(t, dbDir)
	})

	t.Run("chunked stream exceeding 32 MiB rejected with 413", func(t *testing.T) {
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			part, err := mw.CreateFormFile("db", "too-big.db")
			if err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			_, _ = part.Write([]byte("SQLite format 3\x00"))
			chunk := bytes.Repeat([]byte("B"), 64*1024)
			const target = int64(32*1024*1024 + 1024)
			for sent := int64(16); sent < target; sent += int64(len(chunk)) {
				if _, err := part.Write(chunk); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
			_ = mw.Close()
			_ = pw.Close()
		}()

		req, err := http.NewRequest(http.MethodPost, srv.URL+"/panel/api/server/importDB", pr)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.ContentLength = -1
		req.Header.Set("Content-Type", mw.FormDataContentType())

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			t.Fatalf("status = %d, want 413 (body=%s)", resp.StatusCode, string(raw))
		}
		var env hostEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		if env.Success || !strings.Contains(env.Msg, "32 MiB") {
			t.Fatalf("unexpected 413 envelope: %+v", env)
		}
		assertNoControllerImportStagingDirs(t, dbDir)
	})
}

func TestImportDBBadMultipartReturns400AndCleansUp(t *testing.T) {
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("keepHostSettings", "maybe")
	part, _ := mw.CreateFormFile("db", "bad.db")
	_, _ = part.Write([]byte("SQLite format 3\x00"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	newImportTestEngine().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	var env hostEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if env.Success {
		t.Fatal("expected success=false")
	}
	assertNoControllerImportStagingDirs(t, dbDir)
}

func TestImportDBKeepHostSettingsBeforeAndAfterFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub xray binary is a shell script")
	}
	for _, keepBefore := range []bool{true, false} {
		name := "after"
		if keepBefore {
			name = "before"
		}
		t.Run("keepHostSettings_false_"+name, func(t *testing.T) {
			uploadPath := filepath.Join(t.TempDir(), "x-ui.db")
			if err := database.InitDB(uploadPath); err != nil {
				t.Fatalf("InitDB(upload): %v", err)
			}
			if err := (&service.SettingService{}).SetPort(29999); err != nil {
				t.Fatalf("SetPort(upload): %v", err)
			}
			_ = database.CloseDB()
			uploadBytes, err := os.ReadFile(uploadPath)
			if err != nil {
				t.Fatalf("read upload: %v", err)
			}

			newHostTestDB(t)
			if err := (&service.SettingService{}).SetPort(20531); err != nil {
				t.Fatalf("SetPort(host): %v", err)
			}
			binDir := t.TempDir()
			t.Setenv("XUI_BIN_FOLDER", binDir)
			t.Setenv("XUI_LOG_FOLDER", t.TempDir())
			if err := os.WriteFile(filepath.Join(binDir, xray.GetBinaryName()), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
				t.Fatalf("write stub xray: %v", err)
			}
			global.SetRestartHook(func() {})
			t.Cleanup(func() { global.SetRestartHook(func() {}) })

			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			if keepBefore {
				_ = mw.WriteField("keepHostSettings", "false")
			}
			part, _ := mw.CreateFormFile("db", "x-ui.db")
			_, _ = part.Write(uploadBytes)
			if !keepBefore {
				_ = mw.WriteField("keepHostSettings", "false")
			}
			_ = mw.Close()

			req := httptest.NewRequest(http.MethodPost, "/panel/api/server/importDB", &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			w := httptest.NewRecorder()
			newImportTestEngine().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
			}
			port, err := (&service.SettingService{}).GetPort()
			if err != nil {
				t.Fatalf("GetPort: %v", err)
			}
			if port != 29999 {
				t.Fatalf("port = %d, want 29999 from imported DB when keepHostSettings=false", port)
			}
			assertNoControllerImportStagingDirs(t, config.GetDBFolderPath())
		})
	}
}
