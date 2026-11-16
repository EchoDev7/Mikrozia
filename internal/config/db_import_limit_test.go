package config

import (
	"errors"
	"os"
	"testing"
)

func TestGetDBImportMaxBytes(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		prev, had := os.LookupEnv("XUI_DB_IMPORT_MAX_MIB")
		_ = os.Unsetenv("XUI_DB_IMPORT_MAX_MIB")
		t.Cleanup(func() {
			if had {
				_ = os.Setenv("XUI_DB_IMPORT_MAX_MIB", prev)
			} else {
				_ = os.Unsetenv("XUI_DB_IMPORT_MAX_MIB")
			}
		})

		got, err := GetDBImportMaxBytes()
		if err != nil {
			t.Fatalf("GetDBImportMaxBytes() error = %v", err)
		}
		const want = int64(256) * 1024 * 1024
		if got != want {
			t.Fatalf("GetDBImportMaxBytes() = %d, want %d", got, want)
		}
	})

	validCases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "min boundary 32", raw: "32", want: 32 * 1024 * 1024},
		{name: "max boundary 2048", raw: "2048", want: 2048 * 1024 * 1024},
		{name: "trimmed whitespace", raw: " 128\t", want: 128 * 1024 * 1024},
	}
	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XUI_DB_IMPORT_MAX_MIB", tc.raw)
			got, err := GetDBImportMaxBytes()
			if err != nil {
				t.Fatalf("GetDBImportMaxBytes(%q) error = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("GetDBImportMaxBytes(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}

	invalidCases := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "whitespace only", raw: "   "},
		{name: "zero", raw: "0"},
		{name: "negative", raw: "-32"},
		{name: "below min", raw: "31"},
		{name: "above max", raw: "2049"},
		{name: "non-numeric", raw: "abc"},
		{name: "unit suffix", raw: "64MiB"},
		{name: "plus sign", raw: "+64"},
		{name: "hex", raw: "0x40"},
		{name: "overflow", raw: "99999999999999999999999"},
	}
	for _, tc := range invalidCases {
		t.Run("reject "+tc.name, func(t *testing.T) {
			t.Setenv("XUI_DB_IMPORT_MAX_MIB", tc.raw)
			got, err := GetDBImportMaxBytes()
			if !errors.Is(err, ErrInvalidDBImportMaxMiB) {
				t.Fatalf("GetDBImportMaxBytes(%q) = (%d, %v), want ErrInvalidDBImportMaxMiB", tc.raw, got, err)
			}
		})
	}
}

func TestGetSQLiteRestoreMaxStatementBytes(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		prev, had := os.LookupEnv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB")
		_ = os.Unsetenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB")
		t.Cleanup(func() {
			if had {
				_ = os.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", prev)
			} else {
				_ = os.Unsetenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB")
			}
		})

		got, err := GetSQLiteRestoreMaxStatementBytes()
		if err != nil {
			t.Fatalf("GetSQLiteRestoreMaxStatementBytes() error = %v", err)
		}
		const want = int64(16) * 1024 * 1024
		if got != want {
			t.Fatalf("GetSQLiteRestoreMaxStatementBytes() = %d, want %d", got, want)
		}
	})

	validCases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "min boundary 1", raw: "1", want: 1 * 1024 * 1024},
		{name: "max boundary 256", raw: "256", want: 256 * 1024 * 1024},
		{name: "trimmed whitespace", raw: " 32\t", want: 32 * 1024 * 1024},
	}
	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", tc.raw)
			got, err := GetSQLiteRestoreMaxStatementBytes()
			if err != nil {
				t.Fatalf("GetSQLiteRestoreMaxStatementBytes(%q) error = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("GetSQLiteRestoreMaxStatementBytes(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}

	invalidCases := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "whitespace only", raw: "   "},
		{name: "zero", raw: "0"},
		{name: "negative", raw: "-1"},
		{name: "above max", raw: "257"},
		{name: "non-numeric", raw: "abc"},
		{name: "unit suffix", raw: "16MiB"},
		{name: "plus sign", raw: "+16"},
		{name: "hex", raw: "0x10"},
		{name: "overflow", raw: "99999999999999999999999"},
	}
	for _, tc := range invalidCases {
		t.Run("reject "+tc.name, func(t *testing.T) {
			t.Setenv("XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB", tc.raw)
			got, err := GetSQLiteRestoreMaxStatementBytes()
			if !errors.Is(err, ErrInvalidSQLiteRestoreMaxStatementMiB) {
				t.Fatalf("GetSQLiteRestoreMaxStatementBytes(%q) = (%d, %v), want ErrInvalidSQLiteRestoreMaxStatementMiB", tc.raw, got, err)
			}
		})
	}
}
