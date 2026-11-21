package controller

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/netsafe"
	"github.com/mhsanaei/3x-ui/v3/internal/web/entity"
	"github.com/mhsanaei/3x-ui/v3/internal/web/global"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/websocket"

	"github.com/gin-gonic/gin"
)

var filenameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

// ServerController handles server management and status-related operations.
type ServerController struct {
	BaseController

	serverService            service.ServerService
	settingService           service.SettingService
	panelService             panel.PanelService
	xrayMetricsService       service.XrayMetricsService
	routerOSAPIService       service.RouterOSAPIService
	routerOSContainerService service.RouterOSContainerService
}

// NewServerController creates a new ServerController, initializes routes, and starts background tasks.
func NewServerController(g *gin.RouterGroup) *ServerController {
	a := &ServerController{}
	service.RestoreSystemMetrics()
	a.initRouter(g)
	a.startTask()
	return a
}

// initRouter sets up the routes for server status, Xray management, and utility endpoints.
func (a *ServerController) initRouter(g *gin.RouterGroup) {
	g.GET("/status", a.status)
	g.GET("/routerosAPIStatus", a.routerosAPIStatus)
	g.GET("/routerosContainerStatus", a.routerosContainerStatus)
	g.GET("/routerosContainerLogs/:count", a.routerosContainerLogs)
	g.GET("/cpuHistory/:bucket", a.getCpuHistoryBucket)
	g.GET("/history/:metric/:bucket", a.getMetricHistoryBucket)
	g.GET("/xrayMetricsState", a.getXrayMetricsState)
	g.GET("/xrayMetricsHistory/:metric/:bucket", a.getXrayMetricsHistoryBucket)
	g.GET("/xrayObservatory", a.getXrayObservatory)
	g.GET("/xrayObservatoryHistory/:tag/:bucket", a.getXrayObservatoryHistoryBucket)
	g.GET("/getPanelUpdateInfo", a.getPanelUpdateInfo)
	g.GET("/getConfigJson", a.getConfigJson)
	g.GET("/getDb", a.getDb)
	g.GET("/getMigration", a.getMigration)
	g.GET("/getNewUUID", a.getNewUUID)
	g.GET("/getWebCertFiles", a.getWebCertFiles)
	g.GET("/descendants", a.descendants)
	g.GET("/getNewX25519Cert", a.getNewX25519Cert)
	g.GET("/getNewmldsa65", a.getNewmldsa65)
	g.GET("/getNewmlkem768", a.getNewmlkem768)
	g.GET("/getNewVlessEnc", a.getNewVlessEnc)
	g.GET("/clientIps", a.getClientIps)

	g.POST("/stopXrayService", a.stopXrayService)
	g.POST("/restartXrayService", a.restartXrayService)
	g.POST("/restartRouterosContainer", a.restartRouterosContainer)
	g.POST("/logs/:count", a.getLogs)
	g.POST("/xrayRuntimeLogs/:count", a.getXrayRuntimeLogs)
	g.POST("/xraylogs/:count", a.getXrayLogs)
	g.POST("/amneziawglogs/:count", a.getAmneziaWGLogs)
	g.POST("/importDB", a.importDB)
	g.POST("/getNewEchCert", a.getNewEchCert)
	g.POST("/getCertHash", a.getCertHash)
	g.POST("/getRemoteCertHash", a.getRemoteCertHash)
	g.POST("/scanRealityTarget", a.scanRealityTarget)
	g.POST("/scanRealityTargets", a.scanRealityTargets)
	g.POST("/clientIps", a.setClientIps)
}

// startTask registers the @2s ticker that refreshes server status, samples
// xray metrics, and pushes the new snapshot to all websocket subscribers.
// State + sampling live in ServerService; the controller only orchestrates
// the cross-service side effects (xrayMetrics sample + websocket broadcast).
func (a *ServerController) startTask() {
	c := global.GetWebServer().GetCron()
	_, _ = c.AddFunc("@every 2s", func() {
		status := a.serverService.RefreshStatus()
		if status == nil {
			return
		}
		a.xrayMetricsService.Sample(time.Now())
		websocket.BroadcastStatus(status)
	})
	_, _ = c.AddFunc("@every 1m", func() {
		if err := service.PersistSystemMetrics(); err != nil {
			logger.Warning("persist system metrics failed:", err)
		}
	})
}

// status returns the current server status information.
func (a *ServerController) status(c *gin.Context) { jsonObj(c, a.serverService.CurrentStatus(), nil) }

func (a *ServerController) routerosAPIStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	jsonObj(c, a.routerOSAPIService.Status(), nil)
}

func (a *ServerController) routerosContainerStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	jsonObj(c, a.routerOSContainerService.Status(c.Request.Context()), nil)
}

func (a *ServerController) routerosContainerLogs(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	count, err := strconv.Atoi(c.Param("count"))
	if err != nil || count < 1 || count > 100 {
		jsonMsg(c, "invalid count", fmt.Errorf("unsupported count"))
		return
	}
	jsonObj(c, a.routerOSContainerService.Logs(c.Request.Context(), count), nil)
}

func (a *ServerController) restartRouterosContainer(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	jsonObj(c, a.routerOSContainerService.Restart(c.Request.Context()), nil)
}

func parseHistoryBucket(c *gin.Context) (int, bool) {
	bucket, err := strconv.Atoi(c.Param("bucket"))
	if err != nil || bucket <= 0 || !service.IsAllowedHistoryBucket(bucket) {
		jsonMsg(c, "invalid bucket", fmt.Errorf("unsupported bucket"))
		return 0, false
	}
	return bucket, true
}

// getCpuHistoryBucket retrieves aggregated CPU usage history based on the specified time bucket.
// Kept for back-compat; new callers should use /history/cpu/:bucket which
// returns {"t","v"} (uniform across all metrics) instead of {"t","cpu"}.
func (a *ServerController) getCpuHistoryBucket(c *gin.Context) {
	bucket, ok := parseHistoryBucket(c)
	if !ok {
		return
	}
	jsonObj(c, a.serverService.AggregateCpuHistory(bucket, 60), nil)
}

// getMetricHistoryBucket returns up to 60 buckets of history for a single
// system metric (cpu, mem, netUp, netDown, online, load1/5/15). The
// SystemHistoryModal calls one endpoint per active tab.
func (a *ServerController) getMetricHistoryBucket(c *gin.Context) {
	metric := c.Param("metric")
	if !slices.Contains(service.SystemMetricKeys, metric) {
		jsonMsg(c, "invalid metric", fmt.Errorf("unknown metric"))
		return
	}
	bucket, ok := parseHistoryBucket(c)
	if !ok {
		return
	}
	jsonObj(c, a.serverService.AggregateSystemMetric(metric, bucket, 60), nil)
}

func (a *ServerController) getXrayMetricsState(c *gin.Context) {
	jsonObj(c, a.xrayMetricsService.State(), nil)
}

func (a *ServerController) getXrayMetricsHistoryBucket(c *gin.Context) {
	metric := c.Param("metric")
	if !slices.Contains(service.XrayMetricKeys, metric) {
		jsonMsg(c, "invalid metric", fmt.Errorf("unknown metric"))
		return
	}
	bucket, ok := parseHistoryBucket(c)
	if !ok {
		return
	}
	jsonObj(c, a.xrayMetricsService.AggregateMetric(metric, bucket, 60), nil)
}

func (a *ServerController) getXrayObservatory(c *gin.Context) {
	jsonObj(c, a.xrayMetricsService.ObservatorySnapshot(), nil)
}

func (a *ServerController) getXrayObservatoryHistoryBucket(c *gin.Context) {
	tag := c.Param("tag")
	if !a.xrayMetricsService.HasObservatoryTag(tag) {
		jsonMsg(c, "invalid tag", fmt.Errorf("unknown observatory tag"))
		return
	}
	bucket, ok := parseHistoryBucket(c)
	if !ok {
		return
	}
	jsonObj(c, a.xrayMetricsService.AggregateObservatory(tag, bucket, 60), nil)
}

// getPanelUpdateInfo retrieves the current and latest panel version.
func (a *ServerController) getPanelUpdateInfo(c *gin.Context) {
	info, err := a.panelService.GetUpdateInfo()
	if err != nil {
		logger.Debug("panel update check failed:", err)
		c.JSON(http.StatusOK, entity.Msg{Success: false})
		return
	}
	jsonObj(c, info, nil)
}

// stopXrayService stops the Xray service.
func (a *ServerController) stopXrayService(c *gin.Context) {
	err := a.serverService.StopXrayService()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.xray.stopError"), err)
		websocket.BroadcastXrayState("error", err.Error())
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.xray.stopSuccess"), err)
	websocket.BroadcastXrayState("stop", "")
	websocket.BroadcastNotification(
		I18nWeb(c, "pages.xray.stopSuccess"),
		"Xray service has been stopped",
		"warning",
	)
}

// restartXrayService restarts the Xray service.
func (a *ServerController) restartXrayService(c *gin.Context) {
	err := a.serverService.RestartXrayService()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.xray.restartError"), err)
		websocket.BroadcastXrayState("error", err.Error())
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.xray.restartSuccess"), err)
	websocket.BroadcastXrayState("running", "")
	websocket.BroadcastNotification(
		I18nWeb(c, "pages.xray.restartSuccess"),
		"Xray service has been restarted successfully",
		"success",
	)
}

// getLogs retrieves the application logs based on count and level filters.
func (a *ServerController) getLogs(c *gin.Context) {
	logs := a.serverService.GetPanelLogs(c.Param("count"), c.PostForm("level"))
	jsonObj(c, logs, nil)
}

// getXrayLogs retrieves Xray logs with filtering options for direct, blocked, and proxy traffic.
func (a *ServerController) getXrayLogs(c *gin.Context) {
	freedoms, blackholes := a.serverService.GetDefaultLogOutboundTags()
	logs := a.serverService.GetXrayLogs(
		c.Param("count"),
		c.PostForm("filter"),
		c.PostForm("showDirect"),
		c.PostForm("showBlocked"),
		c.PostForm("showProxy"),
		freedoms,
		blackholes,
	)
	jsonObj(c, logs, nil)
}

func (a *ServerController) getXrayRuntimeLogs(c *gin.Context) {
	jsonObj(c, a.serverService.GetXrayRuntimeLogs(c.Param("count"), c.PostForm("level")), nil)
}

// getAmneziaWGLogs retrieves the live AmneziaWG peer activity and the panel's
// own AmneziaWG event lines, optionally narrowed by a free-text filter.
func (a *ServerController) getAmneziaWGLogs(c *gin.Context) {
	logs := a.serverService.GetAmneziaWGLogs(c.Param("count"), c.PostForm("filter"))
	jsonObj(c, logs, nil)
}

// getConfigJson retrieves the Xray configuration as JSON.
func (a *ServerController) getConfigJson(c *gin.Context) {
	configJson, err := a.serverService.GetConfigJson()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.getConfigError"), err)
		return
	}
	jsonObj(c, configJson, nil)
}

// getDb downloads the database file by streaming from a prepared file-backed artifact.
func (a *ServerController) getDb(c *gin.Context) {
	artifact, err := a.serverService.PrepareBackup(c.Request.Context(), c.Request.Host)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.getDatabaseError"), err)
		return
	}
	defer artifact.Cleanup()
	a.streamDatabaseArtifact(c, artifact)
}

// getMigration downloads a cross-engine migration file by streaming from a
// prepared file-backed artifact: a .dump on SQLite or a .db on PostgreSQL.
func (a *ServerController) getMigration(c *gin.Context) {
	artifact, err := a.serverService.PrepareMigration(c.Request.Context())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.getDatabaseError"), err)
		return
	}
	defer artifact.Cleanup()
	a.streamDatabaseArtifact(c, artifact)
}

func (a *ServerController) streamDatabaseArtifact(c *gin.Context, artifact *service.DatabaseArtifact) {
	if !filenameRegex.MatchString(artifact.Filename) {
		_ = c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid filename"))
		return
	}

	f, err := artifact.Open()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.getDatabaseError"), err)
		return
	}
	defer f.Close()

	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", "attachment; filename="+artifact.Filename)
	c.Header("Content-Length", strconv.FormatInt(artifact.Size, 10))
	c.Status(http.StatusOK)
	if _, copyErr := io.Copy(c.Writer, f); copyErr != nil {
		logger.Warningf("database artifact stream interrupted: %v", copyErr)
	}
}

// importDB imports a database file and restarts the Xray service.
func (a *ServerController) importDB(c *gin.Context) {
	maxImportBytes, err := config.GetDBImportMaxBytes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, entity.Msg{
			Success: false,
			Msg:     err.Error(),
			Obj:     nil,
		})
		return
	}

	staged, err := service.StageImportMultipart(c.Request.Context(), c.Request, c.Writer, maxImportBytes)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrImportTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, entity.Msg{
				Success: false,
				Msg:     err.Error(),
				Obj:     nil,
			})
		case errors.Is(err, service.ErrImportBadRequest):
			c.JSON(http.StatusBadRequest, entity.Msg{
				Success: false,
				Msg:     err.Error(),
				Obj:     nil,
			})
		default:
			jsonMsg(c, I18nWeb(c, "pages.index.readDatabaseError"), err)
		}
		return
	}
	defer staged.Cleanup()

	if err := a.serverService.ImportDB(staged, staged.KeepHostSettings); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.index.importDatabaseError"), err)
		return
	}
	// Startup-registered routes (subPath) must match the restored DB, and the
	// browser's restartPanel follow-up can 401 once the imported users land (#6446).
	_ = a.panelService.RestartPanel(3 * time.Second)
	jsonObj(c, I18nWeb(c, "pages.index.importDatabaseSuccess"), nil)
}

// descendants publishes read-only summaries of the nodes this panel manages so
// a parent panel can surface them as transitive sub-nodes in a chained
// topology. Called by the parent via the node's API token (#4983).
func (a *ServerController) descendants(c *gin.Context) {
	data, err := (&service.NodeService{}).LocalDescendants()
	jsonObj(c, data, err)
}

// getWebCertFiles returns this panel's own web TLS certificate and key file
// paths. The central panel calls it on a node (via the node's API token) so
// "Set Cert from Panel" can fill a node-assigned inbound with paths that exist
// on the node's filesystem instead of the central panel's — see issue #4854.
func (a *ServerController) getWebCertFiles(c *gin.Context) {
	certFile, err := a.settingService.GetCertFile()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	keyFile, err := a.settingService.GetKeyFile()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, gin.H{"webCertFile": certFile, "webKeyFile": keyFile}, nil)
}

// getNewX25519Cert generates a new X25519 certificate.
func (a *ServerController) getNewX25519Cert(c *gin.Context) {
	cert, err := a.serverService.GetNewX25519Cert()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.getNewX25519CertError"), err)
		return
	}
	jsonObj(c, cert, nil)
}

// getNewmldsa65 generates a new ML-DSA-65 key.
func (a *ServerController) getNewmldsa65(c *gin.Context) {
	cert, err := a.serverService.GetNewmldsa65()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.getNewmldsa65Error"), err)
		return
	}
	jsonObj(c, cert, nil)
}

// getNewEchCert generates a new ECH certificate for the given SNI.
func (a *ServerController) getNewEchCert(c *gin.Context) {
	cert, err := a.serverService.GetNewEchCert(c.PostForm("sni"))
	if err != nil {
		jsonMsg(c, "get ech certificate", err)
		return
	}
	jsonObj(c, cert, nil)
}

// getCertHash returns the hex SHA-256 of the given certificate (file path or
// inline content) so the panel can fill the pinned-cert field.
func (a *ServerController) getCertHash(c *gin.Context) {
	hashes, err := a.serverService.GetCertHash(c.PostForm("certFile"), c.PostForm("certContent"))
	if err != nil {
		jsonMsg(c, "get cert hash", err)
		return
	}
	jsonObj(c, hashes, nil)
}

// getRemoteCertHash runs `xray tls ping` against the given server and returns
// its live certificate SHA-256 hash(es) for pinning.
func (a *ServerController) getRemoteCertHash(c *gin.Context) {
	allowPrivate := c.PostForm("allowPrivate") == "true"
	hashes, err := a.serverService.GetRemoteCertHash(c.PostForm("server"), allowPrivate)
	if errors.Is(err, netsafe.ErrPrivateAddressBlocked) {
		jsonMsgObj(c, "get remote cert hash", gin.H{"privateTarget": true}, err)
		return
	}
	if err != nil {
		jsonMsg(c, "get remote cert hash", err)
		return
	}
	jsonObj(c, hashes, nil)
}

// scanRealityTarget probes the candidate REALITY target with the given sni and
// returns a feasibility verdict; allowPrivate is the panel's confirmed opt-in.
func (a *ServerController) scanRealityTarget(c *gin.Context) {
	xver, _ := strconv.Atoi(c.PostForm("xver"))
	allowPrivate := c.PostForm("allowPrivate") == "true"
	res, err := a.serverService.ScanRealityTarget(c.PostForm("target"), c.PostForm("sni"), xver, allowPrivate)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.scanRealityTargetError"), err)
		return
	}
	jsonObj(c, res, nil)
}

// scanRealityTargets probes the supplied comma-separated targets, or the
// realityScanCandidates setting when empty, ranked by feasibility then latency.
func (a *ServerController) scanRealityTargets(c *gin.Context) {
	res, err := a.serverService.ScanRealityTargets(c.PostForm("targets"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.scanRealityTargetError"), err)
		return
	}
	jsonObj(c, res, nil)
}

// getNewVlessEnc generates a new VLESS encryption key.
func (a *ServerController) getNewVlessEnc(c *gin.Context) {
	out, err := a.serverService.GetNewVlessEnc()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.getNewVlessEncError"), err)
		return
	}
	jsonObj(c, out, nil)
}

// getNewUUID generates a new UUID.
func (a *ServerController) getNewUUID(c *gin.Context) {
	uuidResp, err := a.serverService.GetNewUUID()
	if err != nil {
		jsonMsg(c, "Failed to generate UUID", err)
		return
	}
	jsonObj(c, uuidResp, nil)
}

// getNewmlkem768 generates a new ML-KEM-768 key.
func (a *ServerController) getNewmlkem768(c *gin.Context) {
	out, err := a.serverService.GetNewmlkem768()
	if err != nil {
		jsonMsg(c, "Failed to generate mlkem768 keys", err)
		return
	}
	jsonObj(c, out, nil)
}

func (a *ServerController) getClientIps(c *gin.Context) {
	ips, err := (&service.InboundService{}).GetAllInboundClientIps()
	jsonObj(c, ips, err)
}

func (a *ServerController) setClientIps(c *gin.Context) {
	var ips []model.InboundClientIps
	if err := c.ShouldBindJSON(&ips); err != nil {
		jsonMsg(c, "invalid data", err)
		return
	}
	err := (&service.InboundService{}).MergeInboundClientIps(ips)
	jsonMsg(c, "Client IPs merged", err)
}
