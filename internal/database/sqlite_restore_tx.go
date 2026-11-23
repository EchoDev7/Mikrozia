package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/logger"
)

const (
	sqliteRestoreStateFilename  = ".mikrozia-sqlite-restore-state.json"
	sqliteRestoreOpDirPrefix    = ".mikrozia-sqlite-restore-"
	sqliteRestoreStateVersion   = 1
	sqliteRestoreCandidateFile  = "candidate.db"
	sqliteRestoreFallbackFile   = "fallback.db"
	sqliteRestorePrevLiveFile   = "previous-live.db"
	sqliteRestoreRecoveryFile   = "recovery.db"
	sqliteRestoreFailedLiveFile = "failed-live.db"
)

type sqliteRestorePhase string

const (
	restorePhasePrepared    sqliteRestorePhase = "prepared"
	restorePhaseApplying    sqliteRestorePhase = "applying"
	restorePhaseVerifying   sqliteRestorePhase = "verifying"
	restorePhaseCommitted   sqliteRestorePhase = "committed"
	restorePhaseRollingBack sqliteRestorePhase = "rolling_back"
	restorePhaseRolledBack  sqliteRestorePhase = "rolled_back"
)

type sqliteRestoreState struct {
	Version      int                `json:"version"`
	Phase        sqliteRestorePhase `json:"phase"`
	TargetDB     string             `json:"target_db"`
	OpDir        string             `json:"op_dir"`
	Candidate    string             `json:"candidate"`
	Fallback     string             `json:"fallback"`
	PrevLive     string             `json:"prev_live"`
	RecoveryCopy string             `json:"recovery_copy"`
	FailedLive   string             `json:"failed_live"`
	OperationID  string             `json:"operation_id"`
	CreatedAt    string             `json:"created_at"`
}

type sqliteRestoreOps struct {
	rename           func(oldpath, newpath string) error
	remove           func(name string) error
	removeAll        func(path string) error
	closeDB          func() error
	initDB           func(dbPath string) error
	validateSQLiteDB func(dbPath string) error
	syncFile         func(path string) error
	syncDir          func(dirPath string) error
	syncTempFile     func(f *os.File) error
	copyFile         func(srcPath, dstPath string) error
}

func defaultSQLiteRestoreOps() sqliteRestoreOps {
	return sqliteRestoreOps{
		rename:           os.Rename,
		remove:           os.Remove,
		removeAll:        os.RemoveAll,
		closeDB:          CloseDB,
		initDB:           InitDB,
		validateSQLiteDB: ValidateSQLiteDB,
		syncFile:         syncFile,
		syncDir:          syncDir,
		syncTempFile:     func(f *os.File) error { return f.Sync() },
		copyFile:         copyFileDurable0600,
	}
}

var (
	sqliteRestoreMu            sync.Mutex
	sqliteRestoreFailpointHook func(point string)
	restoreOps                 = defaultSQLiteRestoreOps()
)

// SQLiteRestoreTransaction coordinates a crash-safe SQLite database replacement
// with durable state tracking, WAL-consistent fallback, and rollback.
type SQLiteRestoreTransaction struct {
	dbPath    string
	dbDir     string
	statePath string
	opDir     string
	state     sqliteRestoreState
	locked    bool
	finished  bool
}

// BeginSQLiteRestore acquires the in-process restore lock, verifies no pending
// restore marker exists, and creates a private 0700 operation directory.
func BeginSQLiteRestore(dbPath string) (*SQLiteRestoreTransaction, error) {
	if IsPostgres() {
		return nil, errors.New("sqlite restore transaction is unavailable for PostgreSQL")
	}
	if dbPath == "" {
		return nil, errors.New("sqlite database path is empty")
	}
	if !sqliteRestoreMu.TryLock() {
		return nil, errors.New("another SQLite restore transaction is already in progress")
	}

	tx := &SQLiteRestoreTransaction{
		dbPath:    dbPath,
		dbDir:     filepath.Dir(dbPath),
		statePath: filepath.Join(filepath.Dir(dbPath), sqliteRestoreStateFilename),
		locked:    true,
	}

	if _, err := os.Lstat(tx.statePath); err == nil {
		tx.releaseLock()
		return nil, fmt.Errorf("a pending SQLite restore state already exists at %s; recover or inspect it before starting another restore", tx.statePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		tx.releaseLock()
		return nil, fmt.Errorf("failed checking SQLite restore state %s: %w", tx.statePath, err)
	}

	opDir, err := os.MkdirTemp(tx.dbDir, sqliteRestoreOpDirPrefix+"*")
	if err != nil {
		tx.releaseLock()
		return nil, fmt.Errorf("failed creating restore operation directory: %w", err)
	}
	if err := os.Chmod(opDir, 0o700); err != nil {
		_ = restoreOps.remove(opDir)
		tx.releaseLock()
		return nil, fmt.Errorf("failed setting restore operation directory permissions: %w", err)
	}
	if err := restoreOps.syncDir(tx.dbDir); err != nil {
		_ = restoreOps.remove(opDir)
		tx.releaseLock()
		return nil, fmt.Errorf("failed syncing database directory: %w", err)
	}

	tx.opDir = opDir
	tx.state = sqliteRestoreState{
		Version:      sqliteRestoreStateVersion,
		Phase:        restorePhasePrepared,
		TargetDB:     filepath.Base(dbPath),
		OpDir:        filepath.Base(opDir),
		Candidate:    sqliteRestoreCandidateFile,
		Fallback:     sqliteRestoreFallbackFile,
		PrevLive:     sqliteRestorePrevLiveFile,
		RecoveryCopy: sqliteRestoreRecoveryFile,
		FailedLive:   sqliteRestoreFailedLiveFile,
		OperationID:  newRestoreOperationID(),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	return tx, nil
}

// CandidatePath returns the candidate database path inside the operation directory.
func (tx *SQLiteRestoreTransaction) CandidatePath() string {
	return filepath.Join(tx.opDir, tx.state.Candidate)
}

// FallbackPath returns the fallback snapshot path inside the operation directory.
func (tx *SQLiteRestoreTransaction) FallbackPath() string {
	return filepath.Join(tx.opDir, tx.state.Fallback)
}

// OpDir returns the private operation directory path.
func (tx *SQLiteRestoreTransaction) OpDir() string {
	return tx.opDir
}

// CloseLiveDB closes the active SQLite database connection pool.
func (tx *SQLiteRestoreTransaction) CloseLiveDB() error {
	return restoreOps.closeDB()
}

// AbortBeforeApply cleans up the pre-apply operation directory and releases the
// restore lock without touching the live database.
func (tx *SQLiteRestoreTransaction) AbortBeforeApply() error {
	if tx == nil || tx.finished {
		return nil
	}
	defer tx.releaseLock()
	tx.finished = true

	var errs []error
	if tx.opDir != "" {
		if rmErr := removeValidatedOpDir(tx.dbDir, tx.state.OpDir); rmErr != nil {
			errs = append(errs, rmErr)
		}
		if syncErr := restoreOps.syncDir(tx.dbDir); syncErr != nil {
			errs = append(errs, syncErr)
		}
	}
	if _, statErr := os.Lstat(tx.statePath); statErr == nil {
		if rmErr := restoreOps.remove(tx.statePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			errs = append(errs, rmErr)
		}
		if syncErr := restoreOps.syncDir(tx.dbDir); syncErr != nil {
			errs = append(errs, syncErr)
		}
	}
	return errors.Join(errs...)
}

// PrepareCandidate stages, validates, prepares for migration, and fsyncs the
// candidate SQLite database inside the operation directory.
func (tx *SQLiteRestoreTransaction) PrepareCandidate(stagedPath string, isSQLDump bool) error {
	candidatePath := tx.CandidatePath()
	if isSQLDump {
		if err := RestoreSQLiteContext(context.Background(), stagedPath, candidatePath); err != nil {
			return fmt.Errorf("Error rebuilding a SQLite database from the migration dump: %w", err)
		}
	} else {
		if err := restoreOps.rename(stagedPath, candidatePath); err != nil {
			return fmt.Errorf("Error moving staged db: %w", err)
		}
	}

	if err := os.Chmod(candidatePath, 0o600); err != nil {
		return fmt.Errorf("failed setting candidate permissions: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		sidecar := candidatePath + suffix
		if _, err := os.Lstat(sidecar); err == nil {
			return fmt.Errorf("candidate database has unexpected sidecar %s", sidecar)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed checking candidate sidecar %s: %w", sidecar, err)
		}
	}
	if err := restoreOps.validateSQLiteDB(candidatePath); err != nil {
		return fmt.Errorf("Invalid or corrupt db file: %w", err)
	}
	if err := PrepareSQLiteForMigration(candidatePath); err != nil {
		return fmt.Errorf("This file cannot be imported: %w", err)
	}
	if err := restoreOps.validateSQLiteDB(candidatePath); err != nil {
		return fmt.Errorf("candidate failed post-preparation validation: %w", err)
	}
	if err := restoreOps.syncFile(candidatePath); err != nil {
		return fmt.Errorf("failed syncing candidate database: %w", err)
	}
	if err := restoreOps.syncDir(tx.opDir); err != nil {
		return fmt.Errorf("failed syncing restore operation directory: %w", err)
	}
	return nil
}

// CreateFallbackSnapshot takes a consistent online SQLite Backup API snapshot
// of the live database (including committed WAL data), validates, and syncs it.
func (tx *SQLiteRestoreTransaction) CreateFallbackSnapshot() error {
	fallbackPath := tx.FallbackPath()
	if err := BackupSQLiteContext(context.Background(), fallbackPath); err != nil {
		return fmt.Errorf("failed creating fallback snapshot: %w", err)
	}
	if err := os.Chmod(fallbackPath, 0o600); err != nil {
		return fmt.Errorf("failed setting fallback snapshot permissions: %w", err)
	}
	if err := restoreOps.validateSQLiteDB(fallbackPath); err != nil {
		return fmt.Errorf("fallback snapshot failed validation: %w", err)
	}
	if err := restoreOps.syncFile(fallbackPath); err != nil {
		return fmt.Errorf("failed syncing fallback snapshot: %w", err)
	}
	if err := restoreOps.syncDir(tx.opDir); err != nil {
		return fmt.Errorf("failed syncing restore operation directory: %w", err)
	}
	return nil
}

// WritePreparedState durably writes the initial prepared state marker.
func (tx *SQLiteRestoreTransaction) WritePreparedState() error {
	tx.state.Phase = restorePhasePrepared
	if err := writeRestoreStateDurable(tx.dbDir, tx.statePath, tx.state); err != nil {
		return err
	}
	triggerRestoreFailpoint("prepared")
	return nil
}

// Apply transitions to applying, preserves the live DB and any sidecars inside
// the operation directory, moves the candidate into place, and marks verifying.
func (tx *SQLiteRestoreTransaction) Apply() error {
	if err := restoreOps.validateSQLiteDB(tx.FallbackPath()); err != nil {
		return fmt.Errorf("refusing to apply without valid fallback snapshot: %w", err)
	}
	tx.state.Phase = restorePhaseApplying
	if err := writeRestoreStateDurable(tx.dbDir, tx.statePath, tx.state); err != nil {
		return err
	}
	triggerRestoreFailpoint("applying")

	prevLivePath := filepath.Join(tx.opDir, tx.state.PrevLive)
	if _, err := os.Lstat(tx.dbPath); err == nil {
		if err := restoreOps.rename(tx.dbPath, prevLivePath); err != nil {
			return fmt.Errorf("Error backing up current db file: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed checking live db before apply: %w", err)
	}

	if err := moveSidecarsToDir(tx.dbPath, prevLivePath); err != nil {
		return fmt.Errorf("failed preserving live db sidecars: %w", err)
	}
	triggerRestoreFailpoint("after_move_live")

	if err := restoreOps.rename(tx.CandidatePath(), tx.dbPath); err != nil {
		return fmt.Errorf("Error moving candidate db file: %w", err)
	}
	triggerRestoreFailpoint("after_move_candidate")

	if err := os.Chmod(tx.dbPath, 0o600); err != nil {
		return fmt.Errorf("failed setting live db permissions: %w", err)
	}
	if err := restoreOps.syncFile(tx.dbPath); err != nil {
		return fmt.Errorf("failed syncing applied live db: %w", err)
	}
	if err := restoreOps.syncDir(tx.dbDir); err != nil {
		return fmt.Errorf("failed syncing database directory after apply: %w", err)
	}
	if err := restoreOps.syncDir(tx.opDir); err != nil {
		return fmt.Errorf("failed syncing operation directory after apply: %w", err)
	}

	tx.state.Phase = restorePhaseVerifying
	if err := writeRestoreStateDurable(tx.dbDir, tx.statePath, tx.state); err != nil {
		return err
	}
	triggerRestoreFailpoint("verifying")
	return nil
}

// MarkPostInitFailpoint fires the post-InitDB pre-commit test failpoint.
func (tx *SQLiteRestoreTransaction) MarkPostInitFailpoint() {
	triggerRestoreFailpoint("after_init_new_db")
}

// ValidateLiveDB runs SQLite integrity validation on the live database file.
func (tx *SQLiteRestoreTransaction) ValidateLiveDB() error {
	return restoreOps.validateSQLiteDB(tx.dbPath)
}

// SyncLiveDB fsyncs the live database file.
func (tx *SQLiteRestoreTransaction) SyncLiveDB() error {
	return restoreOps.syncFile(tx.dbPath)
}

// InitLiveDB opens and initializes the live SQLite database using restoreOps.
func (tx *SQLiteRestoreTransaction) InitLiveDB() error {
	return restoreOps.initDB(tx.dbPath)
}

// Commit durably marks the restore committed before removing the fallback and
// operation directory, then removes the state marker. It owns pre/post durable
// classification for both errors and panics so callers never roll back a committed DB.
func (tx *SQLiteRestoreTransaction) Commit() (durable bool, err error) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		logger.Errorf("SQLite restore commit panic (durable=%v, type=%T)\n%s", durable, r, debug.Stack())
		if durable {
			tx.finished = true
			tx.releaseLock()
			err = errors.New("restore committed durably, but post-commit cleanup encountered an internal error")
			return
		}
		err = errors.New("unexpected internal error while writing committed restore state")
	}()

	tx.state.Phase = restorePhaseCommitted
	if writeErr := writeRestoreStateDurable(tx.dbDir, tx.statePath, tx.state); writeErr != nil {
		return false, fmt.Errorf("failed writing committed state: %w", writeErr)
	}
	durable = true
	triggerRestoreFailpoint("committed_before_cleanup")

	tx.finished = true
	defer tx.releaseLock()
	if cleanupErr := completeCommittedCleanup(tx.dbDir, tx.statePath, tx.state.OpDir); cleanupErr != nil {
		return true, fmt.Errorf("restore committed durably, but post-commit cleanup failed: %w", cleanupErr)
	}
	return true, nil
}

// Rollback restores the fallback snapshot via a recovery copy, reopens the DB,
// validates it, and invokes restartServices before removing recovery artifacts.
func (tx *SQLiteRestoreTransaction) Rollback(causeErr error, restartServices func() error) error {
	defer tx.releaseLock()
	tx.finished = true

	tx.state.Phase = restorePhaseRollingBack
	if stateErr := writeRestoreStateDurable(tx.dbDir, tx.statePath, tx.state); stateErr != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback failed writing rolling_back state: %w", stateErr))
	}
	triggerRestoreFailpoint("rolling_back")

	if closeErr := restoreOps.closeDB(); closeErr != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback failed closing failed db: %w", closeErr))
	}

	failedLiveBase := filepath.Join(tx.opDir, tx.state.FailedLive)
	if err := preserveFailedLive(tx.dbPath, failedLiveBase); err != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback failed preserving failed live db: %w", err))
	}

	if err := restoreLiveFromFallbackCopy(tx.dbDir, tx.opDir, tx.dbPath, tx.state); err != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback failed restoring fallback: %w", err))
	}

	if err := restoreOps.initDB(tx.dbPath); err != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback failed reopening restored db: %w", err))
	}
	if err := restoreOps.validateSQLiteDB(tx.dbPath); err != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback failed validating restored db: %w", err))
	}
	if restartServices != nil {
		if err := restartServices(); err != nil {
			return errors.Join(causeErr, fmt.Errorf("rollback failed restarting services: %w", err))
		}
	}

	tx.state.Phase = restorePhaseRolledBack
	if stateErr := writeRestoreStateDurable(tx.dbDir, tx.statePath, tx.state); stateErr != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback succeeded but failed writing rolled_back state: %w", stateErr))
	}
	triggerRestoreFailpoint("rolled_back_before_cleanup")

	if err := completeCommittedCleanup(tx.dbDir, tx.statePath, tx.state.OpDir); err != nil {
		return errors.Join(causeErr, fmt.Errorf("rollback succeeded but cleanup failed: %w", err))
	}
	return causeErr
}

func (tx *SQLiteRestoreTransaction) releaseLock() {
	if tx != nil && tx.locked {
		tx.locked = false
		sqliteRestoreMu.Unlock()
	}
}

// RecoverPendingSQLiteRestore inspects any pending SQLite restore marker on
// startup (before InitDB) and safely completes, rolls back, or halts on error.
func RecoverPendingSQLiteRestore(dbPath string) error {
	if IsPostgres() || dbPath == "" {
		return nil
	}
	dbDir := filepath.Dir(dbPath)
	statePath := filepath.Join(dbDir, sqliteRestoreStateFilename)

	info, err := os.Lstat(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed inspecting SQLite restore marker %s: %w; do not delete restore artifacts manually", statePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("invalid SQLite restore marker %s (not a regular file); refusing to delete artifacts", statePath)
	}
	if permErr := validateRestoreFilePerm(info, statePath); permErr != nil {
		return fmt.Errorf("invalid SQLite restore marker %s: %w; refusing to delete artifacts", statePath, permErr)
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("failed reading SQLite restore marker %s: %w; do not delete restore artifacts manually", statePath, err)
	}
	var state sqliteRestoreState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("corrupt SQLite restore marker %s: %w; preserve logs and artifacts and do not delete data manually", statePath, err)
	}
	if err := validateRestoreState(dbPath, state); err != nil {
		return fmt.Errorf("invalid SQLite restore marker %s: %w; preserve logs and artifacts and do not delete data manually", statePath, err)
	}

	opDir := filepath.Join(dbDir, state.OpDir)
	opDirInfo, opDirErr := os.Lstat(opDir)
	opDirExists := opDirErr == nil
	if opDirErr != nil && !errors.Is(opDirErr, os.ErrNotExist) {
		return fmt.Errorf("failed inspecting SQLite restore operation directory %s: %w; preserve logs and artifacts and do not delete data manually", opDir, opDirErr)
	}
	if opDirExists {
		if opDirInfo.Mode()&os.ModeSymlink != 0 || !opDirInfo.IsDir() {
			return fmt.Errorf("unsafe SQLite restore operation path %s; preserve logs and artifacts and do not delete data manually", opDir)
		}
		if err := validateExistingOpDirArtifacts(opDir, state); err != nil {
			return fmt.Errorf("unsafe SQLite restore artifacts in %s: %w; preserve logs and artifacts and do not delete data manually", opDir, err)
		}
	}

	switch state.Phase {
	case restorePhasePrepared:
		if isLiveSQLiteHealthy(dbPath) {
			return completeCommittedCleanup(dbDir, statePath, state.OpDir)
		}
		if !opDirExists {
			return fmt.Errorf("startup recovery from %s halted in prepared phase: missing operation directory %s and unhealthy live db; preserve logs and artifacts", statePath, opDir)
		}
		if err := recoverLiveFromFallback(dbDir, opDir, dbPath, state); err != nil {
			return fmt.Errorf("startup recovery from %s failed in prepared phase: %w", statePath, err)
		}
		return completeCommittedCleanup(dbDir, statePath, state.OpDir)

	case restorePhaseApplying, restorePhaseVerifying, restorePhaseRollingBack:
		if !opDirExists {
			return fmt.Errorf("startup recovery from %s halted in %s phase: missing operation directory %s; preserve logs and artifacts", statePath, state.Phase, opDir)
		}
		if err := recoverLiveFromFallback(dbDir, opDir, dbPath, state); err != nil {
			return fmt.Errorf("startup recovery from %s failed in %s phase: %w", statePath, state.Phase, err)
		}
		return completeCommittedCleanup(dbDir, statePath, state.OpDir)

	case restorePhaseCommitted, restorePhaseRolledBack:
		if isLiveSQLiteHealthy(dbPath) {
			return completeCommittedCleanup(dbDir, statePath, state.OpDir)
		}
		if !opDirExists {
			return fmt.Errorf("startup recovery from %s halted in %s phase: unhealthy live db and missing operation directory %s; preserve logs and artifacts", statePath, state.Phase, opDir)
		}
		if err := recoverLiveFromFallback(dbDir, opDir, dbPath, state); err != nil {
			return fmt.Errorf("startup recovery from %s failed in %s phase with unhealthy live db: %w", statePath, state.Phase, err)
		}
		return completeCommittedCleanup(dbDir, statePath, state.OpDir)

	default:
		return fmt.Errorf("unknown SQLite restore phase %q in %s; preserve logs and artifacts", state.Phase, statePath)
	}
}

func recoverLiveFromFallback(dbDir, opDir, dbPath string, state sqliteRestoreState) error {
	failedLiveBase := filepath.Join(opDir, state.FailedLive)
	if err := preserveFailedLive(dbPath, failedLiveBase); err != nil {
		return err
	}
	if err := restoreLiveFromFallbackCopy(dbDir, opDir, dbPath, state); err != nil {
		return err
	}
	if err := restoreOps.validateSQLiteDB(dbPath); err != nil {
		return fmt.Errorf("recovered database failed integrity check: %w", err)
	}
	return nil
}

func restoreLiveFromFallbackCopy(dbDir, opDir, dbPath string, state sqliteRestoreState) error {
	fallbackPath := filepath.Join(opDir, state.Fallback)
	if err := ensureRegularNonSymlink0600(fallbackPath); err != nil {
		return fmt.Errorf("fallback snapshot invalid %s: %w", fallbackPath, err)
	}
	if err := restoreOps.validateSQLiteDB(fallbackPath); err != nil {
		return fmt.Errorf("fallback snapshot corrupt %s: %w", fallbackPath, err)
	}

	recoveryCopy := filepath.Join(opDir, state.RecoveryCopy)
	if _, err := os.Lstat(recoveryCopy); err == nil {
		if rmErr := restoreOps.remove(recoveryCopy); rmErr != nil {
			return fmt.Errorf("failed clearing stale recovery copy %s: %w", recoveryCopy, rmErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed checking recovery copy %s: %w", recoveryCopy, err)
	}

	if err := restoreOps.copyFile(fallbackPath, recoveryCopy); err != nil {
		return fmt.Errorf("failed creating recovery copy: %w", err)
	}
	if err := ensureRegularNonSymlink0600(recoveryCopy); err != nil {
		return fmt.Errorf("recovery copy permissions invalid %s: %w", recoveryCopy, err)
	}
	if err := restoreOps.validateSQLiteDB(recoveryCopy); err != nil {
		return fmt.Errorf("recovery copy failed validation: %w", err)
	}
	if err := restoreOps.syncDir(opDir); err != nil {
		return fmt.Errorf("failed syncing operation directory before recovery rename: %w", err)
	}
	if err := restoreOps.rename(recoveryCopy, dbPath); err != nil {
		return fmt.Errorf("failed moving recovery copy to live db: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("failed setting recovered db permissions: %w", err)
	}
	if err := restoreOps.syncFile(dbPath); err != nil {
		return fmt.Errorf("failed syncing recovered db: %w", err)
	}
	if err := restoreOps.syncDir(dbDir); err != nil {
		return fmt.Errorf("failed syncing database directory after recovery: %w", err)
	}
	return nil
}

func preserveFailedLive(dbPath, failedLiveBase string) error {
	if info, err := os.Lstat(dbPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to move symlinked live db %s", dbPath)
		}
		target := failedLiveBase
		if _, statErr := os.Lstat(target); statErr == nil {
			target = fmt.Sprintf("%s.%d", failedLiveBase, time.Now().UnixNano())
		}
		if err := restoreOps.rename(dbPath, target); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return moveSidecarsToDir(dbPath, failedLiveBase)
}

func moveSidecarsToDir(srcBase, dstBase string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		src := srcBase + suffix
		info, err := os.Lstat(src)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to move symlinked sidecar %s", src)
		}
		dst := dstBase + suffix
		if _, statErr := os.Lstat(dst); statErr == nil {
			dst = fmt.Sprintf("%s.%d%s", dstBase, time.Now().UnixNano(), suffix)
		}
		if err := restoreOps.rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

func completeCommittedCleanup(dbDir, statePath, opDirName string) error {
	if opDirName != "" {
		if err := removeValidatedOpDir(dbDir, opDirName); err != nil {
			return fmt.Errorf("failed cleaning restore operation directory: %w", err)
		}
		if err := restoreOps.syncDir(dbDir); err != nil {
			return fmt.Errorf("failed syncing database directory after opDir removal: %w", err)
		}
	}
	if err := restoreOps.remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed removing restore state marker %s: %w", statePath, err)
	}
	if err := restoreOps.syncDir(dbDir); err != nil {
		return fmt.Errorf("failed syncing database directory after state removal: %w", err)
	}
	return nil
}

func removeValidatedOpDir(dbDir, opDirName string) error {
	if !isSafeSingleName(opDirName) || !strings.HasPrefix(opDirName, sqliteRestoreOpDirPrefix) {
		return fmt.Errorf("refusing to remove invalid operation directory name %q", opDirName)
	}
	opDir := filepath.Join(dbDir, opDirName)
	info, err := os.Lstat(opDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("refusing to remove non-directory or symlink operation path %s", opDir)
	}
	return restoreOps.removeAll(opDir)
}

func validateRestoreState(dbPath string, state sqliteRestoreState) error {
	if state.Version != sqliteRestoreStateVersion {
		return fmt.Errorf("unsupported state version %d", state.Version)
	}
	switch state.Phase {
	case restorePhasePrepared, restorePhaseApplying, restorePhaseVerifying, restorePhaseCommitted, restorePhaseRollingBack, restorePhaseRolledBack:
	default:
		return fmt.Errorf("unknown phase %q", state.Phase)
	}
	if !isSafeSingleName(state.TargetDB) || state.TargetDB != filepath.Base(dbPath) {
		return fmt.Errorf("target_db %q does not match %q", state.TargetDB, filepath.Base(dbPath))
	}
	if !isSafeSingleName(state.OpDir) || !strings.HasPrefix(state.OpDir, sqliteRestoreOpDirPrefix) {
		return fmt.Errorf("invalid op_dir %q", state.OpDir)
	}
	if state.Candidate != sqliteRestoreCandidateFile {
		return fmt.Errorf("unexpected candidate filename %q", state.Candidate)
	}
	if state.Fallback != sqliteRestoreFallbackFile {
		return fmt.Errorf("unexpected fallback filename %q", state.Fallback)
	}
	if state.PrevLive != sqliteRestorePrevLiveFile {
		return fmt.Errorf("unexpected prev_live filename %q", state.PrevLive)
	}
	if state.RecoveryCopy != sqliteRestoreRecoveryFile {
		return fmt.Errorf("unexpected recovery_copy filename %q", state.RecoveryCopy)
	}
	if state.FailedLive != sqliteRestoreFailedLiveFile {
		return fmt.Errorf("unexpected failed_live filename %q", state.FailedLive)
	}
	if !isSafeOperationID(state.OperationID) {
		return fmt.Errorf("invalid operation_id %q", state.OperationID)
	}
	if state.CreatedAt == "" {
		return errors.New("missing created_at timestamp")
	}
	if _, err := time.Parse(time.RFC3339, state.CreatedAt); err != nil {
		return fmt.Errorf("invalid created_at timestamp %q: %w", state.CreatedAt, err)
	}

	dbDir := filepath.Dir(dbPath)
	opDir := filepath.Join(dbDir, state.OpDir)
	if rel, err := filepath.Rel(filepath.Clean(dbDir), filepath.Clean(opDir)); err != nil || rel != state.OpDir {
		return fmt.Errorf("operation directory %s escapes database directory", opDir)
	}
	info, err := os.Lstat(opDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("operation directory %s is a symlink or non-directory", opDir)
		}
		if permErr := validateRestoreDirPerm(info, opDir); permErr != nil {
			return permErr
		}
	}
	return nil
}

func validateExistingOpDirArtifacts(opDir string, state sqliteRestoreState) error {
	for _, name := range []string{state.Candidate, state.Fallback, state.RecoveryCopy} {
		p := filepath.Join(opDir, name)
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		if permErr := validateRestoreFilePerm(info, p); permErr != nil {
			return permErr
		}
	}
	return nil
}

func isSafeOperationID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		isValid := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !isValid {
			return false
		}
	}
	return true
}

func isSafeSingleName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if filepath.IsAbs(name) {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	return filepath.Base(name) == name
}

func isLiveSQLiteHealthy(dbPath string) bool {
	info, err := os.Lstat(dbPath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false
	}
	return restoreOps.validateSQLiteDB(dbPath) == nil
}

func ensureRegularNonSymlink0600(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	return validateRestoreFilePerm(info, path)
}

func writeRestoreStateDurable(dbDir, statePath string, state sqliteRestoreState) error {
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed encoding restore state: %w", err)
	}
	payload = append(payload, '\n')

	tmpFile, err := os.CreateTemp(dbDir, ".mikrozia-sqlite-restore-state-*.tmp")
	if err != nil {
		return fmt.Errorf("failed creating temp restore state file: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = restoreOps.remove(tmpPath)
		}
	}()

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed setting temp restore state permissions: %w", err)
	}
	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed writing temp restore state: %w", err)
	}
	if err := restoreOps.syncTempFile(tmpFile); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed syncing temp restore state: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed closing temp restore state: %w", err)
	}
	if err := restoreOps.rename(tmpPath, statePath); err != nil {
		return fmt.Errorf("failed renaming restore state into place: %w", err)
	}
	cleanupTmp = false
	if err := restoreOps.syncDir(dbDir); err != nil {
		return fmt.Errorf("failed syncing database directory after state write: %w", err)
	}
	return nil
}

func copyFileDurable0600(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cleanupDst := true
	defer func() {
		if cleanupDst {
			_ = os.Remove(dstPath)
		}
	}()

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	cleanupDst = false
	return nil
}

func newRestoreOperationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func triggerRestoreFailpoint(point string) {
	if sqliteRestoreFailpointHook != nil {
		sqliteRestoreFailpointHook(point)
	}
}
