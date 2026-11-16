package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

const (
	importStagingDirPrefix = ".mikrozia-import-"
	importStagedFilename   = "upload.staged"
	importStreamBufferSize = 32 * 1024
	importHeaderSniffBytes = 64
	importMaxFormFieldSize = 64
)

var (
	// ErrImportTooLarge indicates the uploaded database or multipart request exceeded the configured limit.
	ErrImportTooLarge = errors.New("uploaded database exceeds maximum allowed size")
	// ErrImportBadRequest indicates malformed multipart data, missing/duplicate fields, or invalid field values.
	ErrImportBadRequest = errors.New("invalid database import request")
)

// ImportTooLargeError carries the configured maximum upload size for 413 responses.
type ImportTooLargeError struct {
	MaxBytes int64
}

func (e *ImportTooLargeError) Error() string {
	if e == nil || e.MaxBytes <= 0 {
		return ErrImportTooLarge.Error()
	}
	return fmt.Sprintf("uploaded database exceeds the maximum allowed size of %d MiB", e.MaxBytes/(1024*1024))
}

func (e *ImportTooLargeError) Unwrap() error {
	return ErrImportTooLarge
}

// ImportUploadStage holds a streamed, private on-disk upload staged on the
// persistent database mount. Callers must invoke Cleanup when finished.
type ImportUploadStage struct {
	Dir              string
	Path             string
	Size             int64
	Kind             int
	KeepHostSettings bool
	once             sync.Once
}

// Cleanup removes the private .mikrozia-import-* staging directory and its files.
func (s *ImportUploadStage) Cleanup() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.Dir != "" && strings.HasPrefix(filepath.Base(s.Dir), importStagingDirPrefix) {
			_ = os.RemoveAll(s.Dir)
		}
	})
}

// StageImportMultipart streams a multipart/form-data import request directly into
// a private 0700/.0600 staging file under config.GetDBFolderPath() without buffering
// the payload in RAM or spilling into os.TempDir().
func StageImportMultipart(ctx context.Context, r *http.Request, w http.ResponseWriter, maxFileBytes int64) (*ImportUploadStage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.Body == nil {
		return nil, fmt.Errorf("%w: request body is empty", ErrImportBadRequest)
	}
	if maxFileBytes <= 0 {
		return nil, fmt.Errorf("%w: invalid import size limit", config.ErrInvalidDBImportMaxMiB)
	}

	maxRequestBytes := maxFileBytes + config.DBImportMultipartOverheadBytes
	if r.ContentLength > maxRequestBytes {
		return nil, &ImportTooLargeError{MaxBytes: maxFileBytes}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, mapMultipartReadError(ctx, err, maxFileBytes, "invalid multipart request")
	}

	stageDir, err := os.MkdirTemp(config.GetDBFolderPath(), importStagingDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create import staging directory: %w", err)
	}
	if chmodErr := os.Chmod(stageDir, 0o700); chmodErr != nil {
		_ = os.RemoveAll(stageDir)
		return nil, fmt.Errorf("secure import staging directory: %w", chmodErr)
	}

	stage := &ImportUploadStage{
		Dir:              stageDir,
		KeepHostSettings: true,
	}
	succeeded := false
	defer func() {
		if !succeeded {
			stage.Cleanup()
		}
	}()

	var (
		sawDB           bool
		sawKeepSettings bool
		headerBuf       []byte
	)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, mapMultipartReadError(ctx, err, maxFileBytes, "malformed multipart stream")
		}

		formName := part.FormName()
		switch formName {
		case "db":
			if sawDB {
				_ = part.Close()
				return nil, fmt.Errorf("%w: multiple db parts provided", ErrImportBadRequest)
			}
			sawDB = true
			stagedPath := filepath.Join(stageDir, importStagedFilename)
			size, header, streamErr := streamImportPart(ctx, part, stagedPath, maxFileBytes)
			_ = part.Close()
			if streamErr != nil {
				return nil, streamErr
			}
			if size == 0 {
				return nil, fmt.Errorf("%w: uploaded db file is empty", ErrImportBadRequest)
			}
			stage.Path = stagedPath
			stage.Size = size
			headerBuf = header

		case "keepHostSettings":
			if sawKeepSettings {
				_ = part.Close()
				return nil, fmt.Errorf("%w: duplicate keepHostSettings field", ErrImportBadRequest)
			}
			sawKeepSettings = true
			keep, fieldErr := readKeepHostSettingsPart(ctx, part, maxFileBytes)
			_ = part.Close()
			if fieldErr != nil {
				return nil, fieldErr
			}
			stage.KeepHostSettings = keep

		default:
			_ = part.Close()
			return nil, fmt.Errorf("%w: unexpected multipart field %q", ErrImportBadRequest, formName)
		}
	}

	if !sawDB {
		return nil, fmt.Errorf("%w: missing required db file part", ErrImportBadRequest)
	}

	stage.Kind = sniffImportKind(headerBuf)
	succeeded = true
	return stage, nil
}

func readKeepHostSettingsPart(ctx context.Context, r io.Reader, maxFileBytes int64) (bool, error) {
	raw, err := io.ReadAll(io.LimitReader(r, importMaxFormFieldSize+1))
	if err != nil {
		return false, mapMultipartReadError(ctx, err, maxFileBytes, "read keepHostSettings")
	}
	if len(raw) > importMaxFormFieldSize {
		return false, fmt.Errorf("%w: keepHostSettings field exceeds %d bytes", ErrImportBadRequest, importMaxFormFieldSize)
	}
	switch strings.TrimSpace(string(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%w: keepHostSettings must be \"true\" or \"false\"", ErrImportBadRequest)
	}
}

func streamImportPart(ctx context.Context, r io.Reader, dstPath string, maxFileBytes int64) (int64, []byte, error) {
	f, err := os.OpenFile(dstPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, nil, fmt.Errorf("create staged import file: %w", err)
	}
	fileClosed := false
	removeOnErr := true
	defer func() {
		if !fileClosed {
			_ = f.Close()
		}
		if removeOnErr {
			_ = os.Remove(dstPath)
		}
	}()

	if chmodErr := os.Chmod(dstPath, 0o600); chmodErr != nil {
		return 0, nil, fmt.Errorf("secure staged import file: %w", chmodErr)
	}

	lr := io.LimitReader(r, maxFileBytes+1)
	buf := make([]byte, importStreamBufferSize)
	header := make([]byte, 0, importHeaderSniffBytes)
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		n, readErr := lr.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > maxFileBytes {
				return 0, nil, &ImportTooLargeError{MaxBytes: maxFileBytes}
			}
			if len(header) < importHeaderSniffBytes {
				take := min(n, importHeaderSniffBytes-len(header))
				header = append(header, buf[:take]...)
			}
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				return 0, nil, fmt.Errorf("write staged import file: %w", writeErr)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return 0, nil, mapMultipartReadError(ctx, readErr, maxFileBytes, "read uploaded database stream")
		}
	}

	if err := f.Sync(); err != nil {
		return 0, nil, fmt.Errorf("sync staged import file: %w", err)
	}
	if err := f.Close(); err != nil {
		fileClosed = true
		return 0, nil, fmt.Errorf("close staged import file: %w", err)
	}
	fileClosed = true
	removeOnErr = false
	return total, header, nil
}

func mapMultipartReadError(ctx context.Context, err error, maxFileBytes int64, prefix string) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return &ImportTooLargeError{MaxBytes: maxFileBytes}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %s: %w", ErrImportBadRequest, prefix, err)
}
