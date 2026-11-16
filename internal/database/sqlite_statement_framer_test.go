package database

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

func runFramerInChunks(t *testing.T, input []byte, maxBytes int64, chunkSizes []int) ([]string, error) {
	t.Helper()
	var stmts []string
	framer := newSQLiteStatementFramer(maxBytes, func(stmt string, _ int, _, _ int64) error {
		stmts = append(stmts, stmt)
		return nil
	})
	pos := 0
	ci := 0
	for pos < len(input) {
		sz := chunkSizes[ci%len(chunkSizes)]
		ci++
		if sz <= 0 {
			sz = 1
		}
		end := pos + sz
		if end > len(input) {
			end = len(input)
		}
		if err := framer.Feed(input[pos:end]); err != nil {
			return stmts, err
		}
		pos = end
	}
	if err := framer.Finish(); err != nil {
		return stmts, err
	}
	return stmts, nil
}

func TestSQLiteStatementFramerMatrix(t *testing.T) {
	cases := []struct {
		name      string
		sql       string
		wantCount int
		wantErr   error
	}{
		{
			name:      "basic pragma transaction insert",
			sql:       "PRAGMA foreign_keys=OFF;\nBEGIN TRANSACTION;\nCREATE TABLE t(id INT, v TEXT);\nINSERT INTO t VALUES(1,'a;b');\nCOMMIT;\n",
			wantCount: 5,
		},
		{
			name:      "single quoted escaped quotes and semicolon",
			sql:       "INSERT INTO t VALUES('it''s; fine; ''really''');\n",
			wantCount: 1,
		},
		{
			name:      "double quoted identifier with escaped quotes and semicolon",
			sql:       `CREATE TABLE "my""weird;table"("col;1" TEXT);`,
			wantCount: 1,
		},
		{
			name:      "backtick identifier with escaped backtick and semicolon",
			sql:       "CREATE TABLE `bt``;tbl`(`c;1` INT);",
			wantCount: 1,
		},
		{
			name:      "bracket identifier with semicolon",
			sql:       "CREATE TABLE [br;tbl]([col;1] INT);",
			wantCount: 1,
		},
		{
			name:      "blob literal hex",
			sql:       "INSERT INTO t VALUES(X'deadbeef00', x'CAFE');",
			wantCount: 1,
		},
		{
			name:      "line comments LF and CRLF with semicolons",
			sql:       "-- comment; with; semicolons\r\nSELECT 1; -- trailing; comment\r\nSELECT 2;",
			wantCount: 2,
		},
		{
			name:      "line comment at EOF without newline",
			sql:       "SELECT 1;\n-- trailing comment without newline; ignored",
			wantCount: 1,
		},
		{
			name:      "block comments with semicolons stars slashes",
			sql:       "/* block; * / ; */ CREATE TABLE c(x INT); /* mid; */ INSERT INTO c VALUES(1);",
			wantCount: 2,
		},
		{
			name:      "consecutive semicolons and empty statements",
			sql:       ";;; SELECT 1; ; ; SELECT 2;;;",
			wantCount: 2,
		},
		{
			name:      "create trigger with multiple semicolons",
			sql:       "CREATE TRIGGER tr1 AFTER INSERT ON t BEGIN INSERT INTO log VALUES(NEW.id); UPDATE stats SET n = n + 1; END;\nINSERT INTO t VALUES(1);",
			wantCount: 2,
		},
		{
			name:      "create temp and temporary trigger",
			sql:       "CREATE TEMP TRIGGER tr_tmp AFTER DELETE ON t BEGIN DELETE FROM log WHERE id=OLD.id; END;\nCREATE TEMPORARY TRIGGER tr_tmpy BEFORE UPDATE ON t BEGIN SELECT 1; END;",
			wantCount: 2,
		},
		{
			name:      "trigger containing CASE WHEN END semicolon",
			sql:       "CREATE TRIGGER tr_case AFTER INSERT ON t BEGIN UPDATE t SET v = CASE WHEN NEW.id > 0 THEN 'pos' ELSE 'nonpos' END; INSERT INTO log VALUES('done'); END;\nSELECT 1;",
			wantCount: 2,
		},
		{
			name:      "create trigger with comment between CREATE and TRIGGER",
			sql:       "CREATE /* c */ TRIGGER tr_cmt AFTER INSERT ON t BEGIN INSERT INTO log VALUES(1); END; SELECT 1;",
			wantCount: 2,
		},
		{
			name:      "trigger with block comment between semicolon and END",
			sql:       "CREATE TRIGGER tr_bc AFTER INSERT ON t BEGIN INSERT INTO log VALUES(1); /* between ; and END */ END; SELECT 2;",
			wantCount: 2,
		},
		{
			name:      "utf8 multibyte and bom and crlf",
			sql:       "\xef\xbb\xbfINSERT INTO t VALUES('سلام دنیا 🚀; تست');\r\nSELECT 'پایان';\r\n",
			wantCount: 2,
		},
		{
			name:    "whitespace and comments only",
			sql:     "\xef\xbb\xbf   \r\n-- just a comment;\n/* and block; */\n   ",
			wantErr: ErrEmptySQLiteDump,
		},
		{
			name:    "only semicolons",
			sql:     " ; ; \n ; ",
			wantErr: ErrEmptySQLiteDump,
		},
		{
			name:    "incomplete sql missing semicolon",
			sql:     "SELECT 1;\nINSERT INTO t VALUES(1)",
			wantErr: ErrIncompleteSQL,
		},
		{
			name:    "unclosed single quote",
			sql:     "INSERT INTO t VALUES('unterminated;",
			wantErr: ErrIncompleteSQL,
		},
		{
			name:    "unclosed double quote",
			sql:     `CREATE TABLE "unterminated(a INT);`,
			wantErr: ErrIncompleteSQL,
		},
		{
			name:    "unclosed backtick",
			sql:     "CREATE TABLE `unterminated(a INT);",
			wantErr: ErrIncompleteSQL,
		},
		{
			name:    "unclosed bracket",
			sql:     "CREATE TABLE [unterminated(a INT);",
			wantErr: ErrIncompleteSQL,
		},
		{
			name:    "unclosed block comment",
			sql:     "SELECT 1; /* unterminated",
			wantErr: ErrIncompleteSQL,
		},
		{
			name:    "unclosed trigger missing END",
			sql:     "CREATE TRIGGER tr_bad AFTER INSERT ON t BEGIN INSERT INTO log VALUES(1);",
			wantErr: ErrIncompleteSQL,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.sql)

			// 1. Whole input
			wholeStmts, wholeErr := runFramerInChunks(t, raw, 1024*1024, []int{len(raw) + 1})
			if tc.wantErr != nil {
				if !errors.Is(wholeErr, tc.wantErr) {
					t.Fatalf("whole input err = %v, want %v", wholeErr, tc.wantErr)
				}
			} else {
				if wholeErr != nil {
					t.Fatalf("whole input unexpected err = %v", wholeErr)
				}
				if len(wholeStmts) != tc.wantCount {
					t.Fatalf("whole input got %d statements, want %d: %#v", len(wholeStmts), tc.wantCount, wholeStmts)
				}
			}

			// 2. 1-byte chunks
			byteStmts, byteErr := runFramerInChunks(t, raw, 1024*1024, []int{1})
			if tc.wantErr != nil {
				if !errors.Is(byteErr, tc.wantErr) {
					t.Fatalf("1-byte chunks err = %v, want %v", byteErr, tc.wantErr)
				}
			} else {
				if byteErr != nil {
					t.Fatalf("1-byte chunks unexpected err = %v", byteErr)
				}
				if !reflect.DeepEqual(byteStmts, wholeStmts) {
					t.Fatalf("1-byte chunks mismatch:\ngot:  %#v\nwant: %#v", byteStmts, wholeStmts)
				}
			}

			// 3. Seeded random chunk sizes
			rng := rand.New(rand.NewSource(42))
			randChunks := make([]int, 16)
			for i := range randChunks {
				randChunks[i] = 1 + rng.Intn(7)
			}
			randStmts, randErr := runFramerInChunks(t, raw, 1024*1024, randChunks)
			if tc.wantErr != nil {
				if !errors.Is(randErr, tc.wantErr) {
					t.Fatalf("random chunks err = %v, want %v", randErr, tc.wantErr)
				}
			} else {
				if randErr != nil {
					t.Fatalf("random chunks unexpected err = %v", randErr)
				}
				if !reflect.DeepEqual(randStmts, wholeStmts) {
					t.Fatalf("random chunks mismatch:\ngot:  %#v\nwant: %#v", randStmts, wholeStmts)
				}
			}
		})
	}
}

func TestSQLiteStatementFramerTargetedSplitsAndLimits(t *testing.T) {
	t.Run("split at trigger keywords and end semicolon", func(t *testing.T) {
		sql := "CREATE TRIGGER tr_split AFTER INSERT ON t BEGIN UPDATE t SET x = CASE WHEN 1=1 THEN 2 ELSE 3 END; INSERT INTO log VALUES('a'); END;"
		for splitAt := 1; splitAt < len(sql); splitAt++ {
			var got []string
			framer := newSQLiteStatementFramer(1024*1024, func(stmt string, _ int, _, _ int64) error {
				got = append(got, stmt)
				return nil
			})
			if err := framer.Feed([]byte(sql[:splitAt])); err != nil {
				t.Fatalf("splitAt=%d Feed(1) err=%v", splitAt, err)
			}
			if err := framer.Feed([]byte(sql[splitAt:])); err != nil {
				t.Fatalf("splitAt=%d Feed(2) err=%v", splitAt, err)
			}
			if err := framer.Finish(); err != nil {
				t.Fatalf("splitAt=%d Finish err=%v", splitAt, err)
			}
			if len(got) != 1 || got[0] != sql {
				t.Fatalf("splitAt=%d got=%#v, want [%q]", splitAt, got, sql)
			}
		}
	})

	t.Run("statement too large error", func(t *testing.T) {
		big := "INSERT INTO t VALUES('" + strings.Repeat("A", 128) + "');"
		err := streamSQLiteDumpStatements(context.Background(), strings.NewReader(big), 64, func(string, int, int64, int64) error {
			return nil
		})
		if !errors.Is(err, ErrStatementTooLarge) {
			t.Fatalf("got err = %v, want ErrStatementTooLarge", err)
		}
		var stl *StatementTooLargeError
		if !errors.As(err, &stl) {
			t.Fatalf("expected *StatementTooLargeError, got %T", err)
		}
		if stl.StatementIndex != 1 || stl.Line != 1 {
			t.Fatalf("unexpected StatementTooLargeError metadata: %+v", stl)
		}
	})

	t.Run("stream canceled via context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := streamSQLiteDumpStatements(ctx, bytes.NewReader([]byte("SELECT 1;")), 1024, func(string, int, int64, int64) error {
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got err = %v, want context.Canceled", err)
		}
	})

	t.Run("reader error propagated", func(t *testing.T) {
		boom := errors.New("disk read boom")
		err := streamSQLiteDumpStatements(context.Background(), &failingReader{err: boom}, 1024, func(string, int, int64, int64) error {
			return nil
		})
		if !errors.Is(err, boom) {
			t.Fatalf("got err = %v, want %v", err, boom)
		}
	})

	t.Run("quote closed and comment markers at exact chunk boundaries", func(t *testing.T) {
		chunks := [][]byte{
			[]byte("INSERT INTO t VALUES('abc'"),
			[]byte("); --"),
			[]byte(" line comment\nSELECT /*"),
			[]byte(" ok */ 1; -- trailing no newline"),
		}
		var stmts []string
		framer := newSQLiteStatementFramer(1024*1024, func(stmt string, _ int, _, _ int64) error {
			stmts = append(stmts, stmt)
			return nil
		})
		for _, ch := range chunks {
			if err := framer.Feed(ch); err != nil {
				t.Fatalf("Feed(%q): %v", ch, err)
			}
		}
		if err := framer.Finish(); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		if len(stmts) != 2 {
			t.Fatalf("got %d statements, want 2: %#v", len(stmts), stmts)
		}
	})
}

func TestSQLiteStatementFramer_RealSQLiteExecution(t *testing.T) {
	dir := t.TempDir()
	dumpPath := dir + "/framer_exec.sql"
	dstPath := dir + "/framer_exec.db"

	sqlDump := strings.Join([]string{
		"CREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT);",
		"CREATE TABLE log(note TEXT);",
		"CREATE /* inline */ TRIGGER tr_real AFTER INSERT ON t BEGIN INSERT INTO log VALUES(NEW.v); /* after semi */ END; INSERT INTO t VALUES(1, 'from-same-chunk');",
		"INSERT INTO t VALUES(2, 'row2'); -- trailing comment without newline",
	}, "\n")

	if err := writeFileForTest(dumpPath, sqlDump); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSQLiteContext(context.Background(), dumpPath, dstPath); err != nil {
		t.Fatalf("RestoreSQLiteContext: %v", err)
	}
	db := openRawSQLiteForTest(t, dstPath)
	var tCount, logCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&tCount); err != nil || tCount != 2 {
		t.Fatalf("t count=%d err=%v", tCount, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM log`).Scan(&logCount); err != nil || logCount != 2 {
		t.Fatalf("log count=%d err=%v", logCount, err)
	}
}

func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

type failingReader struct {
	err error
}

func (r *failingReader) Read([]byte) (int, error) {
	return 0, r.err
}

var _ io.Reader = (*failingReader)(nil)
