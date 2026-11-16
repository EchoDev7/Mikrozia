package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

func TestGetLogs_UsesApplicationLogsAndIgnoresJournalctl(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "journalctl-called")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake journalctl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	logger.Errorf("mikrozia-service-log-test-error")

	got := (&ServerService{}).GetLogs("10", "error")
	if !strings.Contains(strings.Join(got, "\n"), "mikrozia-service-log-test-error") {
		t.Fatalf("GetLogs(\"10\", \"error\") = %q, want error entry", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("GetLogs invoked journalctl; expected in-memory application logs only")
	}
}

func TestGetLogs_ValidatesCountAndNormalizesLevel(t *testing.T) {
	logger.Infof("mikrozia-info-entry-for-filter-check")
	logger.Errorf("mikrozia-error-entry-for-filter-check")

	svc := &ServerService{}

	for _, badCount := range []string{"0", "-5", "abc", "10001"} {
		if got := svc.GetLogs(badCount, "error"); len(got) != 0 {
			t.Fatalf("GetLogs(%q, \"error\") = %v, want empty", badCount, got)
		}
	}

	if got := svc.GetLogs("10", "bogus"); len(got) != 0 {
		t.Fatalf("GetLogs(\"10\", \"bogus\") = %v, want empty", got)
	}

	emptyLevel := strings.Join(svc.GetLogs("20", ""), "\n")
	if !strings.Contains(emptyLevel, "mikrozia-info-entry-for-filter-check") ||
		!strings.Contains(emptyLevel, "mikrozia-error-entry-for-filter-check") {
		t.Fatalf("GetLogs(\"20\", \"\") = %q, want default info level returning both info and error", emptyLevel)
	}

	for _, errLevel := range []string{"error", "err"} {
		joined := strings.Join(svc.GetLogs("20", errLevel), "\n")
		if !strings.Contains(joined, "mikrozia-error-entry-for-filter-check") {
			t.Fatalf("GetLogs(\"20\", %q) = %q, want error entry included", errLevel, joined)
		}
		if strings.Contains(joined, "mikrozia-info-entry-for-filter-check") {
			t.Fatalf("GetLogs(\"20\", %q) = %q, must not include info entries", errLevel, joined)
		}
	}
}
