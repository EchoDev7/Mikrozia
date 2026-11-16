package database

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	// ErrStatementTooLarge indicates a single SQL statement exceeded the configured limit.
	ErrStatementTooLarge = errors.New("SQLite statement exceeds maximum allowed size")
	// ErrIncompleteSQL indicates the SQLite dump ended mid-statement, mid-quote, or mid-comment.
	ErrIncompleteSQL = errors.New("incomplete SQLite statement at end of dump")
	// ErrEmptySQLiteDump indicates the dump contained no executable SQL statements.
	ErrEmptySQLiteDump = errors.New("SQLite dump contains no SQL statements")
)

// StatementTooLargeError reports the statement index, position, and configured limit.
type StatementTooLargeError struct {
	MaxBytes       int64
	StatementIndex int
	Line           int64
	Offset         int64
}

func (e *StatementTooLargeError) Error() string {
	return fmt.Sprintf(
		"SQLite statement #%d at line %d (byte %d) exceeds the maximum statement size of %d MiB; set XUI_SQLITE_RESTORE_MAX_STATEMENT_MIB to a higher value (up to 256 MiB) only if your container has sufficient RAM",
		e.StatementIndex, e.Line, e.Offset, e.MaxBytes/(1024*1024),
	)
}

func (e *StatementTooLargeError) Unwrap() error {
	return ErrStatementTooLarge
}

type framerLexMode uint8

const (
	lexTop framerLexMode = iota
	lexSingleQuote
	lexDoubleQuote
	lexBacktick
	lexBracket
	lexLineComment
	lexBlockComment
)

// completeState mirrors SQLite's sqlite3_complete() state machine (complete.c)
// for incremental statement framing without splitting on newlines or raw semicolons.
type completeState uint8

const (
	csInvalid completeState = iota
	csFirst
	csCreate
	csCreateTemp
	csNormal
	csTrigger
	csTriggerSemi
	csTriggerEnd
)

var utf8BOM = [3]byte{0xEF, 0xBB, 0xBF}

// sqliteStatementFramer incrementally frames complete SQLite SQL statements
// from arbitrary byte chunks with O(N) time and memory bounded by maxStatementBytes.
type sqliteStatementFramer struct {
	maxStatementBytes int64
	onStatement       func(stmt string, index int, line, offset int64) error

	bomChecking bool
	bomPos      int

	lexMode           framerLexMode
	pendingDash       bool
	pendingSlash      bool
	pendingStar       bool
	pendingQuoteClose bool

	cs         completeState
	stmtActive bool
	stmtBuf    []byte

	wordBuf [16]byte
	wordLen int

	line          int64
	byteOffset    int64
	stmtCount     int
	stmtStartLine int64
	stmtStartByte int64
}

func newSQLiteStatementFramer(maxStatementBytes int64, onStatement func(stmt string, index int, line, offset int64) error) *sqliteStatementFramer {
	return &sqliteStatementFramer{
		maxStatementBytes: maxStatementBytes,
		onStatement:       onStatement,
		bomChecking:       true,
		line:              1,
	}
}

func (f *sqliteStatementFramer) Feed(chunk []byte) error {
	for _, b := range chunk {
		if err := f.feedByte(b); err != nil {
			return err
		}
	}
	return nil
}

func (f *sqliteStatementFramer) Finish() error {
	if f.bomChecking {
		f.bomChecking = false
		for i := 0; i < f.bomPos; i++ {
			if err := f.processByte(utf8BOM[i]); err != nil {
				return err
			}
		}
	}
	if f.pendingDash {
		f.pendingDash = false
		if err := f.finishDash(); err != nil {
			return err
		}
	}
	if f.pendingSlash {
		f.pendingSlash = false
		if err := f.finishSlash(); err != nil {
			return err
		}
	}
	if f.pendingQuoteClose {
		f.pendingQuoteClose = false
		f.lexMode = lexTop
	}
	if f.wordLen > 0 {
		f.flushWord()
	}

	switch f.lexMode {
	case lexSingleQuote, lexDoubleQuote, lexBacktick, lexBracket:
		return fmt.Errorf("%w: unclosed quote or identifier at line %d (byte %d)", ErrIncompleteSQL, f.line, f.byteOffset)
	case lexBlockComment:
		return fmt.Errorf("%w: unclosed block comment at line %d (byte %d)", ErrIncompleteSQL, f.line, f.byteOffset)
	}
	if f.stmtActive && f.cs != csInvalid {
		return fmt.Errorf("%w: statement #%d starting at line %d (byte %d) is missing terminating semicolon", ErrIncompleteSQL, f.stmtCount+1, f.stmtStartLine, f.stmtStartByte)
	}
	if f.stmtCount == 0 {
		return ErrEmptySQLiteDump
	}
	return nil
}

func (f *sqliteStatementFramer) feedByte(b byte) error {
	if f.bomChecking {
		if b == utf8BOM[f.bomPos] {
			f.bomPos++
			f.byteOffset++
			if f.bomPos == len(utf8BOM) {
				f.bomChecking = false
			}
			return nil
		}
		f.bomChecking = false
		for i := 0; i < f.bomPos; i++ {
			if err := f.processByte(utf8BOM[i]); err != nil {
				return err
			}
		}
	}
	f.byteOffset++
	err := f.processByte(b)
	if b == '\n' {
		f.line++
	}
	return err
}

func (f *sqliteStatementFramer) processByte(b byte) error {
	switch f.lexMode {
	case lexLineComment:
		if f.stmtActive {
			if err := f.appendStmtByte(b); err != nil {
				return err
			}
		}
		if b == '\n' {
			f.lexMode = lexTop
		}
		return nil

	case lexBlockComment:
		if f.stmtActive {
			if err := f.appendStmtByte(b); err != nil {
				return err
			}
		}
		if f.pendingStar {
			if b == '/' {
				f.pendingStar = false
				f.lexMode = lexTop
			} else if b != '*' {
				f.pendingStar = false
			}
		} else if b == '*' {
			f.pendingStar = true
		}
		return nil

	case lexSingleQuote:
		return f.processQuoteByte(b, '\'')

	case lexDoubleQuote:
		return f.processQuoteByte(b, '"')

	case lexBacktick:
		return f.processQuoteByte(b, '`')

	case lexBracket:
		if err := f.appendStmtByte(b); err != nil {
			return err
		}
		if b == ']' {
			f.lexMode = lexTop
		}
		return nil
	}

	// lexTop: resolve pending '-' or '/' first.
	if f.pendingDash {
		f.pendingDash = false
		if b == '-' {
			if f.stmtActive {
				if err := f.appendStmtByte('-'); err != nil {
					return err
				}
				if err := f.appendStmtByte('-'); err != nil {
					return err
				}
			}
			f.lexMode = lexLineComment
			return nil
		}
		if err := f.finishDash(); err != nil {
			return err
		}
	}
	if f.pendingSlash {
		f.pendingSlash = false
		if b == '*' {
			if f.stmtActive {
				if err := f.appendStmtByte('/'); err != nil {
					return err
				}
				if err := f.appendStmtByte('*'); err != nil {
					return err
				}
			}
			f.lexMode = lexBlockComment
			return nil
		}
		if err := f.finishSlash(); err != nil {
			return err
		}
	}

	if isSQLiteIdentChar(b) {
		f.ensureStmtStarted()
		if err := f.appendStmtByte(b); err != nil {
			return err
		}
		if f.wordLen < len(f.wordBuf) {
			f.wordBuf[f.wordLen] = b
		}
		f.wordLen++
		return nil
	}

	if f.wordLen > 0 {
		f.flushWord()
	}

	if isSQLiteSpace(b) {
		if f.stmtActive {
			return f.appendStmtByte(b)
		}
		return nil
	}

	switch b {
	case '-':
		f.pendingDash = true
		return nil
	case '/':
		f.pendingSlash = true
		return nil
	case '\'':
		f.ensureStmtStarted()
		f.transitionNonKeywordToken()
		f.lexMode = lexSingleQuote
		return f.appendStmtByte(b)
	case '"':
		f.ensureStmtStarted()
		f.transitionNonKeywordToken()
		f.lexMode = lexDoubleQuote
		return f.appendStmtByte(b)
	case '`':
		f.ensureStmtStarted()
		f.transitionNonKeywordToken()
		f.lexMode = lexBacktick
		return f.appendStmtByte(b)
	case '[':
		f.ensureStmtStarted()
		f.transitionNonKeywordToken()
		f.lexMode = lexBracket
		return f.appendStmtByte(b)
	case ';':
		return f.handleSemicolon()
	default:
		f.ensureStmtStarted()
		f.transitionNonKeywordToken()
		return f.appendStmtByte(b)
	}
}

func (f *sqliteStatementFramer) processQuoteByte(b, q byte) error {
	if f.pendingQuoteClose {
		f.pendingQuoteClose = false
		if b == q {
			return f.appendStmtByte(b)
		}
		f.lexMode = lexTop
		return f.processByte(b)
	}
	if err := f.appendStmtByte(b); err != nil {
		return err
	}
	if b == q {
		f.pendingQuoteClose = true
	}
	return nil
}

func (f *sqliteStatementFramer) finishDash() error {
	f.ensureStmtStarted()
	f.transitionNonKeywordToken()
	return f.appendStmtByte('-')
}

func (f *sqliteStatementFramer) finishSlash() error {
	f.ensureStmtStarted()
	f.transitionNonKeywordToken()
	return f.appendStmtByte('/')
}

func (f *sqliteStatementFramer) handleSemicolon() error {
	if !f.stmtActive || f.cs == csInvalid {
		return nil
	}
	if err := f.appendStmtByte(';'); err != nil {
		return err
	}
	switch f.cs {
	case csTrigger, csTriggerSemi:
		f.cs = csTriggerSemi
		return nil
	default:
		f.stmtCount++
		stmt := string(f.stmtBuf)
		startLine := f.stmtStartLine
		startByte := f.stmtStartByte
		f.resetStatement()
		if f.onStatement != nil {
			return f.onStatement(stmt, f.stmtCount, startLine, startByte)
		}
		return nil
	}
}

func (f *sqliteStatementFramer) ensureStmtStarted() {
	if f.stmtActive {
		return
	}
	f.stmtActive = true
	f.stmtStartLine = f.line
	f.stmtStartByte = f.byteOffset
}

func (f *sqliteStatementFramer) appendStmtByte(b byte) error {
	if f.maxStatementBytes > 0 && int64(len(f.stmtBuf))+1 > f.maxStatementBytes {
		return &StatementTooLargeError{
			MaxBytes:       f.maxStatementBytes,
			StatementIndex: f.stmtCount + 1,
			Line:           f.stmtStartLine,
			Offset:         f.stmtStartByte,
		}
	}
	f.stmtBuf = append(f.stmtBuf, b)
	return nil
}

func (f *sqliteStatementFramer) resetStatement() {
	f.cs = csInvalid
	f.stmtActive = false
	if cap(f.stmtBuf) > 1<<20 {
		f.stmtBuf = nil
	} else {
		f.stmtBuf = f.stmtBuf[:0]
	}
}

func (f *sqliteStatementFramer) transitionNonKeywordToken() {
	switch f.cs {
	case csInvalid, csFirst, csCreate, csCreateTemp:
		f.cs = csNormal
	case csTriggerSemi, csTriggerEnd:
		f.cs = csTrigger
	}
}

func (f *sqliteStatementFramer) flushWord() {
	n := f.wordLen
	f.wordLen = 0
	var word string
	if n <= len(f.wordBuf) {
		word = string(f.wordBuf[:n])
	}

	switch f.cs {
	case csInvalid, csFirst:
		switch {
		case strings.EqualFold(word, "EXPLAIN"):
			f.cs = csFirst
		case strings.EqualFold(word, "CREATE"):
			f.cs = csCreate
		default:
			f.cs = csNormal
		}
	case csCreate:
		switch {
		case strings.EqualFold(word, "TEMP"), strings.EqualFold(word, "TEMPORARY"):
			f.cs = csCreateTemp
		case strings.EqualFold(word, "TRIGGER"):
			f.cs = csTrigger
		default:
			f.cs = csNormal
		}
	case csCreateTemp:
		if strings.EqualFold(word, "TRIGGER") {
			f.cs = csTrigger
		} else {
			f.cs = csNormal
		}
	case csTriggerSemi:
		if strings.EqualFold(word, "END") {
			f.cs = csTriggerEnd
		} else {
			f.cs = csTrigger
		}
	case csTriggerEnd:
		f.cs = csTrigger
	}
}

func isSQLiteIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_' || b == '$' || b >= 0x80
}

func isSQLiteSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

// streamSQLiteDumpStatements reads dumpReader in fixed 32 KiB chunks and invokes
// onStatement for each complete SQLite statement without buffering the whole dump.
func streamSQLiteDumpStatements(ctx context.Context, r io.Reader, maxStatementBytes int64, onStatement func(stmt string, index int, line, offset int64) error) error {
	framer := newSQLiteStatementFramer(maxStatementBytes, onStatement)
	buf := make([]byte, 32*1024)
	for {
		if ctx != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
		}
		n, err := r.Read(buf)
		if n > 0 {
			if feedErr := framer.Feed(buf[:n]); feedErr != nil {
				return feedErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	if ctx != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return framer.Finish()
}
