package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/echodev7/mikrozia/v3/internal/config"
	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
	"github.com/echodev7/mikrozia/v3/internal/util/crypto"
	"github.com/echodev7/mikrozia/v3/internal/web/entity"
	"github.com/echodev7/mikrozia/v3/internal/web/middleware"
	"github.com/echodev7/mikrozia/v3/internal/web/service"
	"github.com/echodev7/mikrozia/v3/internal/web/service/discord"
	"github.com/echodev7/mikrozia/v3/internal/web/service/email"
	"github.com/echodev7/mikrozia/v3/internal/web/service/panel"
	"github.com/echodev7/mikrozia/v3/internal/web/session"

	"github.com/gin-gonic/gin"
)

// updateUserForm represents the form for updating user credentials.
type updateUserForm struct {
	OldUsername   string `json:"oldUsername" form:"oldUsername"`
	OldPassword   string `json:"oldPassword" form:"oldPassword"`
	NewUsername   string `json:"newUsername" form:"newUsername"`
	NewPassword   string `json:"newPassword" form:"newPassword"`
	TwoFactorCode string `json:"twoFactorCode" form:"twoFactorCode"`
}

// updateSettingForm carries the persisted settings plus request-scoped fields
// that must never land in the settings table: the 2FA confirmation code and
// the explicit clear flags for redacted secrets (a blank secret alone means
// "unchanged", so clearing needs its own signal — see #5724).
type updateSettingForm struct {
	entity.AllSetting
	TwoFactorCode            string `json:"twoFactorCode" form:"twoFactorCode"`
	ClearTgBotToken          bool   `json:"clearTgBotToken" form:"clearTgBotToken"`
	ClearLdapPassword        bool   `json:"clearLdapPassword" form:"clearLdapPassword"`
	ClearSmtpPassword        bool   `json:"clearSmtpPassword" form:"clearSmtpPassword"`
	ClearDiscordBotToken     bool   `json:"clearDiscordBotToken" form:"clearDiscordBotToken"`
	ClearRouterosNatPassword bool   `json:"clearRouterosNatPassword" form:"clearRouterosNatPassword"`
}

type testRouterosNatForm struct {
	Host     string `json:"host" form:"host"`
	Port     int    `json:"port" form:"port"`
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

type validateRegexForm struct {
	Regex string `json:"regex" form:"regex"`
}

// SettingController handles settings and user management operations.
type SettingController struct {
	settingService     service.SettingService
	userService        panel.UserService
	panelService       panel.PanelService
	apiTokenService    panel.ApiTokenService
	xrayService        service.XrayService
	inboundService         service.InboundService
	routerOSNATService     service.RouterOSNATService
	routerOSUpdaterService service.RouterOSUpdaterService
}

// NewSettingController creates a new SettingController and initializes its routes.
func NewSettingController(g *gin.RouterGroup) *SettingController {
	a := &SettingController{}
	a.initRouter(g)
	return a
}

// initRouter sets up the routes for settings management.
func (a *SettingController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/setting")

	g.POST("/all", a.getAllSetting)
	g.POST("/defaultSettings", a.getDefaultSettings)
	g.POST("/factoryDefaults", a.getFactoryDefaults)
	g.POST("/update", a.updateSetting)
	g.POST("/validateRegex", a.validateRegex)
	g.POST("/updateUser", a.updateUser)
	g.POST("/adminCredentialState", a.adminCredentialState)
	g.POST("/restartPanel", a.restartPanel)
	g.GET("/getDefaultJsonConfig", a.getDefaultXrayConfig)
	g.GET("/apiTokens", a.listApiTokens)
	g.POST("/apiTokens/create", a.createApiToken)
	g.POST("/apiTokens/delete/:id", a.deleteApiToken)
	g.POST("/apiTokens/setEnabled/:id", a.setApiTokenEnabled)
	g.POST("/testSmtp", a.testSmtp)
	g.POST("/testTgBot", a.testTgBot)
	g.POST("/tgBotCapabilities", a.tgBotCapabilities)
	g.POST("/testDiscord", a.testDiscord)
	g.POST("/testRouterosNat", a.testRouterosNat)
	g.POST("/syncRouterosNat", a.syncRouterosNat)
	g.POST("/detectRouterosWanInterface", a.detectRouterosWanInterface)
	g.GET("/routerosNatRules", a.listRouterosNatRules)
	g.POST("/cleanupRouterosNat", a.cleanupRouterosNat)
	g.GET("/routerosDiagnostics", a.routerosDiagnostics)
	g.POST("/syncRouterosServices", a.syncRouterosServices)
	g.GET("/routerosUpdateInfo", a.routerosUpdateInfo)
	g.POST("/routerosUpdateStage", a.routerosUpdateStage)
	g.POST("/routerosUpdateCutover", a.routerosUpdateCutover)
	g.POST("/routerosUpdateCleanupPrevious", a.routerosUpdateCleanupPrevious)
	g.GET("/detectContainerIp", a.detectContainerIp)
}

func (a *SettingController) validateRegex(c *gin.Context) {
	form := &validateRegexForm{}
	if err := c.ShouldBind(form); err != nil {
		pureJsonMsg(c, http.StatusOK, false, err.Error())
		return
	}
	if err := service.ValidateRegex(form.Regex); err != nil {
		pureJsonMsg(c, http.StatusOK, false, err.Error())
		return
	}
	pureJsonMsg(c, http.StatusOK, true, "")
}

// getAllSetting retrieves all current settings as the browser-safe view:
// secret values are redacted and surfaced as has* presence flags instead.
func (a *SettingController) getAllSetting(c *gin.Context) {
	allSetting, err := a.settingService.GetAllSettingView()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, allSetting, nil)
}

// getDefaultSettings retrieves the default settings based on the host.
func (a *SettingController) getDefaultSettings(c *gin.Context) {
	result, err := a.settingService.GetDefaultSettings(c.Request.Host)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, result, nil)
}

func (a *SettingController) getFactoryDefaults(c *gin.Context) {
	jsonObj(c, a.settingService.GetFactoryDefaults(), nil)
}

// updateSetting updates all settings with the provided data.
func (a *SettingController) updateSetting(c *gin.Context) {
	form, ok := middleware.BindAndValidate[updateSettingForm](c)
	if !ok {
		return
	}
	allSetting := &form.AllSetting
	oldTwoFactor, twoFactorErr := a.settingService.GetTwoFactorEnable()
	oldPanelOutbound, _ := a.settingService.GetPanelOutbound()
	oldTgEnable, _ := a.settingService.GetTgbotEnabled()
	oldTgToken, _ := a.settingService.GetTgBotToken()
	oldTgChatId, _ := a.settingService.GetTgBotChatId()
	oldTgAPIServer, _ := a.settingService.GetTgBotAPIServer()
	oldDiscordEnable, _ := a.settingService.GetDiscordBotEnable()
	oldDiscordToken, _ := a.settingService.GetDiscordBotToken()
	oldDiscordChannelId, _ := a.settingService.GetDiscordChannelId()
	oldDiscordRunTime, _ := a.settingService.GetDiscordRunTime()
	if twoFactorErr == nil && oldTwoFactor {
		// Rebinding the authenticator is the same class of change as turning 2FA
		// off, so both need a current code. Blank still means "unchanged".
		submittedToken := strings.TrimSpace(allSetting.TwoFactorToken)
		storedToken, _ := a.settingService.GetTwoFactorToken()
		if !allSetting.TwoFactorEnable || (submittedToken != "" && submittedToken != storedToken) {
			if err := a.settingService.VerifyTwoFactorCode(form.TwoFactorCode); err != nil {
				jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
				return
			}
		}
	}
	err := a.settingService.UpdateAllSetting(allSetting, service.SecretClears{
		TgBotToken:          form.ClearTgBotToken,
		LdapPassword:        form.ClearLdapPassword,
		SmtpPassword:        form.ClearSmtpPassword,
		DiscordBotToken:     form.ClearDiscordBotToken,
		RouterosNatPassword: form.ClearRouterosNatPassword,
	})
	if err == nil && twoFactorErr == nil && !oldTwoFactor && allSetting.TwoFactorEnable {
		if bumpErr := a.userService.BumpLoginEpoch(); bumpErr != nil {
			err = bumpErr
		}
	}
	if err == nil && form.PanelOutbound != oldPanelOutbound {
		// The egress bridge lives in the generated config; reconcile the
		// running core. One SOCKS inbound plus one routing rule — both
		// hot-appliable, so this normally does not restart Xray.
		if applyErr := a.xrayService.RestartXray(false); applyErr != nil {
			logger.Warning("apply panel outbound change failed:", applyErr)
		}
	}
	// UpdateAllSetting already restored a redacted-blank token, so allSetting.TgBotToken is the effective value to compare.
	if err == nil && reloadTgbotFunc != nil {
		tgChanged := oldTgEnable != allSetting.TgBotEnable ||
			(allSetting.TgBotEnable && (oldTgToken != allSetting.TgBotToken ||
				oldTgChatId != allSetting.TgBotChatId ||
				oldTgAPIServer != allSetting.TgBotAPIServer))
		if tgChanged {
			reloadTgbotFunc()
		}
	}
	if err == nil && reloadDiscordFunc != nil {
		discordChanged := oldDiscordEnable != allSetting.DiscordBotEnable ||
			oldDiscordRunTime != allSetting.DiscordRunTime ||
			(allSetting.DiscordBotEnable && (oldDiscordToken != allSetting.DiscordBotToken ||
				oldDiscordChannelId != allSetting.DiscordChannelId))
		if discordChanged {
			reloadDiscordFunc()
		}
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
}

// AdminCredentialView is the browser-safe state of the administrator account.
// It never carries the username or the password.
type AdminCredentialView struct {
	// Source names where the credential comes from: environment, preserved,
	// legacy-default, or missing.
	Source string `json:"source" example:"environment"`
	// Managed reports that the container environment owns the credential.
	Managed bool `json:"managed" example:"true"`
}

// adminCredentialState reports where the administrator credential comes from,
// so the panel can show the truth instead of offering a competing change.
func (a *SettingController) adminCredentialState(c *gin.Context) {
	state, err := a.userService.InspectAdminAccount()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, AdminCredentialView{Source: string(state.Source), Managed: config.AdminCredentialMutationForbidden()}, nil)
}

// updateUser updates the current user's username and password.
func (a *SettingController) updateUser(c *gin.Context) {
	// The container environment owns the administrator account, so a panel-side
	// change would be overwritten on the next start.
	if config.AdminCredentialMutationForbidden() {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifyUserError"), errors.New(I18nWeb(c, "pages.settings.toasts.adminCredentialManagedByEnvironment")))
		return
	}
	form := &updateUserForm{}
	err := c.ShouldBind(form)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
		return
	}
	user := session.GetLoginUser(c)
	if user.Username != form.OldUsername || !crypto.CheckPasswordHash(user.Password, form.OldPassword) {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifyUserError"), errors.New(I18nWeb(c, "pages.settings.toasts.originalUserPassIncorrect")))
		return
	}
	if form.NewUsername == "" || form.NewPassword == "" {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifyUserError"), errors.New(I18nWeb(c, "pages.settings.toasts.userPassMustBeNotEmpty")))
		return
	}
	if err := a.settingService.VerifyTwoFactorCode(form.TwoFactorCode); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifyUserError"), err)
		return
	}
	err = a.userService.UpdateUser(user.Id, form.NewUsername, form.NewPassword)
	if err == nil {
		user.Username = form.NewUsername
		user.Password, _ = crypto.HashPasswordAsBcrypt(form.NewPassword)
		if saveErr := session.SetLoginUser(c, user); saveErr != nil {
			err = saveErr
		}
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifyUser"), err)
}

// restartPanel restarts the panel service after a delay.
func (a *SettingController) restartPanel(c *gin.Context) {
	err := a.panelService.RestartPanel(time.Second * 3)
	jsonMsg(c, I18nWeb(c, "pages.settings.restartPanelSuccess"), err)
}

// getDefaultXrayConfig retrieves the default Xray configuration.
func (a *SettingController) getDefaultXrayConfig(c *gin.Context) {
	defaultJsonConfig, err := a.settingService.GetDefaultXrayConfig()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, defaultJsonConfig, nil)
}

type apiTokenCreateForm struct {
	Name      string `json:"name" form:"name"`
	Scope     string `json:"scope" form:"scope"`
	ExpiresAt int64  `json:"expiresAt" form:"expiresAt"`
}

type apiTokenEnabledForm struct {
	Enabled       bool   `json:"enabled" form:"enabled"`
	ExpectedScope string `json:"expectedScope" form:"expectedScope"`
}

type apiTokenScopeForm struct {
	ExpectedScope string `json:"expectedScope" form:"expectedScope"`
}

func (a *SettingController) listApiTokens(c *gin.Context) {
	rows, err := a.apiTokenService.List()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.getSettings"), err)
		return
	}
	jsonObj(c, rows, nil)
}

func (a *SettingController) createApiToken(c *gin.Context) {
	form := &apiTokenCreateForm{}
	if err := c.ShouldBind(form); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
		return
	}
	row, err := a.apiTokenService.Create(form.Name, form.Scope, form.ExpiresAt)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
		return
	}
	jsonObj(c, row, nil)
}

func (a *SettingController) deleteApiToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
		return
	}
	form := &apiTokenScopeForm{}
	if bindErr := c.ShouldBind(form); bindErr != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), bindErr)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), a.apiTokenService.DeleteExpectedScope(id, form.ExpectedScope))
}

func (a *SettingController) setApiTokenEnabled(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), err)
		return
	}
	form := &apiTokenEnabledForm{}
	if bindErr := c.ShouldBind(form); bindErr != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), bindErr)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.toasts.modifySettings"), a.apiTokenService.SetEnabledExpectedScope(id, form.ExpectedScope, form.Enabled))
}

func (a *SettingController) testSmtp(c *gin.Context) {
	if emailService == nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.smtpNotInitialized"), errors.New("email service not available"))
		return
	}
	logger.Info("SMTP test: starting...")
	result := emailService.TestConnection()
	if !result.Success {
		logger.Warning("SMTP test failed at", result.Stage+":", result.Message)
		c.JSON(200, gin.H{
			"success": false,
			"stage":   result.Stage,
			"msg":     result.Message,
		})
		return
	}
	logger.Info("SMTP test: success")
	c.JSON(200, gin.H{
		"success": true,
		"stage":   result.Stage,
		"msg":     result.Message,
	})
}

func (a *SettingController) testTgBot(c *gin.Context) {
	enabled, err := a.settingService.GetTgbotEnabled()
	if err != nil || !enabled {
		jsonMsg(c, I18nWeb(c, "pages.settings.tgBotNotEnabled"), errors.New("telegram bot disabled"))
		return
	}
	// Import tgbot package would create a circular dependency, so we call
	// the test through the global function registered at startup.
	if testTgFunc != nil {
		if err := testTgFunc(); err != nil {
			jsonMsg(c, I18nWeb(c, "pages.settings.tgTestFailed")+": "+err.Error(), err)
			return
		}
		jsonMsg(c, I18nWeb(c, "pages.settings.tgTestSuccess"), nil)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.tgBotNotRunning"), errors.New("bot not started"))
}

// tgBotCapabilities reports what the bot can do. Inline mode is a BotFather
// switch the API can read but never set, so the panel warns when lists can't open.
func (a *SettingController) tgBotCapabilities(c *gin.Context) {
	enabled, err := a.settingService.GetTgbotEnabled()
	if err != nil || !enabled {
		jsonMsg(c, I18nWeb(c, "pages.settings.tgBotNotEnabled"), errors.New("telegram bot disabled"))
		return
	}
	if tgCapsFunc == nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tgBotNotRunning"), errors.New("bot not started"))
		return
	}
	caps, err := tgCapsFunc()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.tgTestFailed")+": "+err.Error(), err)
		return
	}
	jsonObj(c, caps, nil)
}

// TgBotCapabilities is the browser-safe shape of the bot's own capabilities.
type TgBotCapabilities struct {
	InlineEnabled bool `json:"inlineEnabled" example:"true"`
	// GroupPrivacy off means the bot reads every group message; when it is on,
	// a bare /start in a group is invisible to the bot.
	GroupPrivacy bool   `json:"groupPrivacy" example:"false"`
	Username     string `json:"username" example:"xui_bot"`
	Running      bool   `json:"running" example:"true"`
}

// tgCapsFunc is wired from the web layer; importing tgbot here is circular.
var tgCapsFunc func() (TgBotCapabilities, error)

// SetTgBotCapsFunc registers the capability probe.
func SetTgBotCapsFunc(fn func() (TgBotCapabilities, error)) { tgCapsFunc = fn }

// testTgFunc is set from web layer to test Telegram sending without circular imports.
var testTgFunc func() error

// SetTestTgFunc registers the function used to test Telegram sending.
func SetTestTgFunc(fn func() error) { testTgFunc = fn }

// reloadTgbotFunc is wired from the web layer; importing tgbot here would be a circular dependency.
var reloadTgbotFunc func()

func SetReloadTgbotFunc(fn func()) { reloadTgbotFunc = fn }

// emailService is set from web layer.
var emailService *email.EmailService

// SetEmailService registers the email service for test endpoints.
func SetEmailService(s *email.EmailService) { emailService = s }

// reloadDiscordFunc is wired from the web layer to reschedule or cancel Discord notify job.
var reloadDiscordFunc func()

func SetReloadDiscordFunc(fn func()) { reloadDiscordFunc = fn }

// discordService is set from web layer.
var discordService *discord.DiscordService

// SetDiscordService registers the Discord service for test endpoints.
func SetDiscordService(s *discord.DiscordService) { discordService = s }

func (a *SettingController) testDiscord(c *gin.Context) {
	if discordService == nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.discordNotInitialized"), errors.New("discord service not available"))
		return
	}
	enabled, err := a.settingService.GetDiscordBotEnable()
	if err != nil || !enabled {
		jsonMsg(c, I18nWeb(c, "pages.settings.discordBotNotEnabled"), errors.New("discord bot disabled"))
		return
	}
	if err := discordService.SendTest(c.Request.Context()); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.discordTestFailed")+": "+err.Error(), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.discordTestSuccess"), nil)
}

func (a *SettingController) testRouterosNat(c *gin.Context) {
	form := &testRouterosNatForm{}
	_ = c.ShouldBind(form)
	var override *routeros.NATConnConfig
	if strings.TrimSpace(form.Host) != "" || form.Port > 0 || strings.TrimSpace(form.Username) != "" || form.Password != "" {
		override = &routeros.NATConnConfig{
			Host:     form.Host,
			Port:     form.Port,
			Username: form.Username,
			Password: form.Password,
		}
	}
	res, err := a.routerOSNATService.TestConnection(c.Request.Context(), override)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if res.State != "connected" {
		detail := res.Detail
		if detail == "" {
			detail = res.State
		}
		jsonMsgObj(c, detail, res, errors.New(detail))
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosNatTestSuccess"), res, nil)
}

func (a *SettingController) syncRouterosNat(c *gin.Context) {
	user := session.GetLoginUser(c)
	inbounds, err := a.inboundService.GetInbounds(user.Id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	res, err := a.routerOSNATService.SyncAllInbounds(c.Request.Context(), inbounds)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if res.Failed > 0 {
		jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosNatSyncPartial"), res, service.JoinErrors(res.Errors))
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosNatSyncSuccess"), res, nil)
}

func (a *SettingController) detectContainerIp(c *gin.Context) {
	jsonObj(c, gin.H{"ip": service.DetectContainerIPv4()}, nil)
}

func (a *SettingController) detectRouterosWanInterface(c *gin.Context) {
	form := &testRouterosNatForm{}
	_ = c.ShouldBind(form)
	var override *routeros.NATConnConfig
	if strings.TrimSpace(form.Host) != "" || form.Port > 0 || strings.TrimSpace(form.Username) != "" || form.Password != "" {
		override = &routeros.NATConnConfig{
			Host:     form.Host,
			Port:     form.Port,
			Username: form.Username,
			Password: form.Password,
		}
	}
	iface, state, err := a.routerOSNATService.DetectWANInterface(c.Request.Context(), override)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if state != "ok" || iface == "" {
		jsonMsgObj(c, state, gin.H{"interface": "", "state": state}, errors.New(state))
		return
	}
	jsonObj(c, gin.H{"interface": iface, "state": "ok"}, nil)
}

func (a *SettingController) listRouterosNatRules(c *gin.Context) {
	rules, err := a.routerOSNATService.ListManagedRules(c.Request.Context())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, rules, nil)
}

func (a *SettingController) cleanupRouterosNat(c *gin.Context) {
	user := session.GetLoginUser(c)
	inbounds, err := a.inboundService.GetInbounds(user.Id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	removed, err := a.routerOSNATService.CleanupOrphanedRules(c.Request.Context(), inbounds)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosNatCleanupSuccess"), gin.H{"removed": removed}, nil)
}

func (a *SettingController) routerosDiagnostics(c *gin.Context) {
	user := session.GetLoginUser(c)
	inbounds, err := a.inboundService.GetInbounds(user.Id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	res, err := a.routerOSNATService.RunDiagnostics(c.Request.Context(), inbounds)
	if err != nil {
		jsonMsgObj(c, err.Error(), res, err)
		return
	}
	jsonObj(c, res, nil)
}

func (a *SettingController) syncRouterosServices(c *gin.Context) {
	if err := a.routerOSNATService.SyncServicePorts(c.Request.Context()); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.routerosNatServicesSyncSuccess"), nil)
}

func (a *SettingController) routerosUpdateInfo(c *gin.Context) {
	force := c.Query("refresh") == "1" || c.Query("refresh") == "true"
	info := a.routerOSUpdaterService.GetUpdateInfo(c.Request.Context(), force)
	jsonObj(c, info, nil)
}

func (a *SettingController) routerosUpdateStage(c *gin.Context) {
	var body struct {
		TargetVersion string `json:"targetVersion" form:"targetVersion"`
	}
	if err := c.ShouldBind(&body); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	spec, err := a.routerOSUpdaterService.StageUpdate(c.Request.Context(), body.TargetVersion)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosUpdateStageStarted"), spec, nil)
}

func (a *SettingController) routerosUpdateCutover(c *gin.Context) {
	var body struct {
		CleanupPrevious bool `json:"cleanupPrevious" form:"cleanupPrevious"`
	}
	_ = c.ShouldBind(&body)
	state, detail, err := a.routerOSUpdaterService.CutoverUpdate(c.Request.Context(), body.CleanupPrevious)
	if err != nil {
		jsonMsgObj(c, err.Error(), gin.H{"state": state, "detail": detail}, err)
		return
	}
	if state == "permission_required" {
		jsonMsgObj(c, "permission_required", gin.H{"state": state, "detail": detail}, errors.New("permission_required"))
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosUpdateCutoverScheduled"), gin.H{"state": "ok"}, nil)
}

func (a *SettingController) routerosUpdateCleanupPrevious(c *gin.Context) {
	removed, err := a.routerOSUpdaterService.CleanupPrevious(c.Request.Context())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsgObj(c, I18nWeb(c, "pages.settings.routerosUpdateCleanupSuccess"), gin.H{"removed": removed}, nil)
}
