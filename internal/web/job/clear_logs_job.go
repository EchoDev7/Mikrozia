package job

import (
	"os"

	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/xray"
)

const defaultMaxXrayLogBytes int64 = 64 << 20

var maxXrayLogBytes = defaultMaxXrayLogBytes

// ClearLogsJob clears old log files to prevent disk space issues.
type ClearLogsJob struct{}

// PruneXrayLogsJob truncates oversized Xray access and error logs.
// PruneXrayLogsJob truncates the Xray access and error logs once either exceeds maxXrayLogBytes.
type PruneXrayLogsJob struct{}

// NewClearLogsJob creates a new log cleanup job instance.
func NewClearLogsJob() *ClearLogsJob {
	return new(ClearLogsJob)
}

// NewPruneXrayLogsJob creates a new Xray log pruning job instance.
func NewPruneXrayLogsJob() *PruneXrayLogsJob {
	return new(PruneXrayLogsJob)
}

// Here Run is an interface method of the Job interface
func (j *ClearLogsJob) Run() {
	wipeXrayLogs()
}

func (j *PruneXrayLogsJob) Run() {
	truncateXrayLog(xray.GetAccessLogPath, maxXrayLogBytes)
	truncateXrayLog(xray.GetErrorLogPath, maxXrayLogBytes)
}

func wipeXrayLogs() {
	truncateXrayLog(xray.GetAccessLogPath, 0)
	truncateXrayLog(xray.GetErrorLogPath, 0)
}

func truncateXrayLog(pathFn func() (string, error), maxBytes int64) {
	logPath, err := pathFn()
	if err != nil || disabledLogPath(logPath) {
		return
	}
	if maxBytes > 0 {
		info, err := os.Stat(logPath)
		if err != nil {
			if !os.IsNotExist(err) {
				logger.Warning("Failed to stat Xray log:", logPath, "-", err)
			}
			return
		}
		if info.Size() <= maxBytes {
			return
		}
	}
	if err := os.Truncate(logPath, 0); err != nil && !os.IsNotExist(err) {
		logger.Warning("Failed to truncate Xray log:", logPath, "-", err)
	}
}

func disabledLogPath(path string) bool {
	return path == "" || path == "none"
}
