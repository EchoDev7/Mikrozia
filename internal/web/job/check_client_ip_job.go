package job

import (
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// onlineUsersSource is the one service.XrayService method the scan needs, so
// tests can drive Run end to end without a live xray core.
type onlineUsersSource interface {
	GetOnlineUsers() ([]xray.OnlineUser, bool, error)
}

// CheckClientIpJob records the source addresses each client is connecting from.
// The addresses come from the core's online-stats API; no access log is
// involved. The job only tracks and displays them: RouterOS Container cannot
// run fail2ban or change host firewall rules, so no IP-limit enforcement is
// attempted and stored limitIp values are left untouched.
type CheckClientIpJob struct {
	onlineUsers onlineUsersSource
	lastIpPrune int64
}

const ipStaleAfterSeconds = int64(30 * 60)

// pruneStaleIpRows cadence; the scan itself cannot prune offline clients' rows.
const ipPruneIntervalSeconds = int64(5 * 60)

// NewCheckClientIpJob creates a new client IP monitoring job instance.
func NewCheckClientIpJob() *CheckClientIpJob {
	return &CheckClientIpJob{onlineUsers: &service.XrayService{}}
}

func (j *CheckClientIpJob) Run() {
	j.pruneStaleIpRows()
	observed, apiMode := j.collectFromOnlineAPI()
	if !apiMode {
		// xray is down or predates the online-stats API. There is no access-log
		// fallback anymore, so there is nothing to do this run.
		logger.Debug("[LimitIP] online-stats API unavailable this run; skipping")
		return
	}
	j.processObserved(observed, true)
}

// collectFromOnlineAPI builds per-email IP observations (email -> ip ->
// last-seen unix seconds) from the core's online-stats API. ok=false means the
// API is unavailable — xray not running, an older core, or a transient gRPC
// failure — and the caller skips the run (there is no access-log fallback).
func (j *CheckClientIpJob) collectFromOnlineAPI() (map[string]map[string]int64, bool) {
	onlineUsers, ok, err := j.onlineUsers.GetOnlineUsers()
	if err != nil {
		logger.Debug("[LimitIP] online-stats API unavailable this run:", err)
		return nil, false
	}
	if !ok {
		return nil, false
	}
	now := time.Now().Unix()
	observed := make(map[string]map[string]int64, len(onlineUsers))
	for _, user := range onlineUsers {
		for _, entry := range user.IPs {
			// No localhost guard needed here: the core's OnlineMap.AddIP drops
			// 127.0.0.1/[::1] itself, so they never reach this list.
			ts := entry.LastSeen
			if ts <= 0 {
				ts = now
			}
			if _, exists := observed[user.Email]; !exists {
				observed[user.Email] = make(map[string]int64)
			}
			if existing, seen := observed[user.Email][entry.IP]; !seen || ts > existing {
				observed[user.Email][entry.IP] = ts
			}
		}
	}
	return observed, true
}

const ipScanChunk = 400

func chunkEmails(s []string, size int) [][]string {
	if len(s) == 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(s)+size-1)/size)
	for size < len(s) {
		s, chunks = s[size:], append(chunks, s[:size])
	}
	return append(chunks, s)
}

// loadInboundsByEmails resolves each email's owning inbound through the
// clients/client_inbounds relation in chunked queries. Like the old per-email
// First() it keeps the lowest inbound id when a client spans several inbounds.
func (j *CheckClientIpJob) loadInboundsByEmails(emails []string) map[string]*model.Inbound {
	db := database.GetDB()
	minInboundByEmail := make(map[string]int, len(emails))
	for _, batch := range chunkEmails(emails, ipScanChunk) {
		var pairs []struct {
			Email     string
			InboundId int
		}
		if err := db.Table("client_inbounds").
			Select("clients.email AS email, client_inbounds.inbound_id AS inbound_id").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Where("clients.email IN ?", batch).
			Scan(&pairs).Error; err != nil {
			j.checkError(err)
			return nil
		}
		for _, p := range pairs {
			if cur, ok := minInboundByEmail[p.Email]; !ok || p.InboundId < cur {
				minInboundByEmail[p.Email] = p.InboundId
			}
		}
	}
	if len(minInboundByEmail) == 0 {
		return nil
	}

	idSet := make(map[int]struct{}, len(minInboundByEmail))
	ids := make([]int, 0, len(minInboundByEmail))
	for _, id := range minInboundByEmail {
		if _, seen := idSet[id]; !seen {
			idSet[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	inboundsById := make(map[int]*model.Inbound, len(ids))
	for lo := 0; lo < len(ids); lo += ipScanChunk {
		hi := min(lo+ipScanChunk, len(ids))
		var page []*model.Inbound
		if err := db.Model(&model.Inbound{}).Where("id IN ?", ids[lo:hi]).Find(&page).Error; err != nil {
			j.checkError(err)
			return nil
		}
		for _, ib := range page {
			inboundsById[ib.Id] = ib
		}
	}

	out := make(map[string]*model.Inbound, len(minInboundByEmail))
	for email, id := range minInboundByEmail {
		if ib, ok := inboundsById[id]; ok {
			out[email] = ib
		}
	}
	return out
}

func (j *CheckClientIpJob) loadClientIpRows(emails []string) map[string]*model.InboundClientIps {
	db := database.GetDB()
	out := make(map[string]*model.InboundClientIps, len(emails))
	for _, batch := range chunkEmails(emails, ipScanChunk) {
		var rows []model.InboundClientIps
		if err := db.Where("client_email IN ?", batch).Find(&rows).Error; err != nil {
			j.checkError(err)
			continue
		}
		for i := range rows {
			out[rows[i].ClientEmail] = &rows[i]
		}
	}
	return out
}

// processObserved runs collection for one scan's observations (email -> ip ->
// last-seen unix seconds). observedAreLive marks the observations as live
// connections, which bypass the stale cutoff: a connection that opened hours
// ago is still live even though its timestamp is old. The online-stats API
// always reports live connections, so the job passes true.
// Lookups are batched up front and all inbound_client_ips writes share one
// transaction, so a scan costs a handful of queries and one fsync instead of
// several per observed email.
func (j *CheckClientIpJob) processObserved(observed map[string]map[string]int64, observedAreLive bool) {
	now := time.Now().Unix()

	emails := make([]string, 0, len(observed))
	for email := range observed {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	inboundByEmail := j.loadInboundsByEmails(emails)
	ipRowByEmail := j.loadClientIpRows(emails)

	// attribution accumulates this scan's local observations per email so they can
	// be recorded under this panel's own guid for cross-node IP attribution.
	attribution := make(map[string][]model.ClientIpEntry, len(observed))

	db := database.GetDB()
	tx := db.Begin()
	if tx.Error != nil {
		j.checkError(tx.Error)
		return
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()

	for _, email := range emails {
		ipTimestamps := observed[email]

		// The observations can still reference a client that was just renamed
		// or deleted; its email no longer matches any inbound. Skip it (and
		// drop any orphaned tracking row) instead of recreating a row and
		// logging an ERROR every run (#4963). The batch map resolves through
		// the clients relation; the per-email fallback keeps its settings LIKE
		// net for clients not yet present there.
		if _, known := inboundByEmail[email]; !known {
			if _, err := j.getInboundByEmail(email); err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					logger.Debugf("[LimitIP] skipping stale observed email %q (renamed or deleted)", email)
					j.delInboundClientIps(tx, email)
				} else {
					j.checkError(err)
				}
				continue
			}
		}

		ipsWithTime := make([]model.ClientIpEntry, 0, len(ipTimestamps))
		attrEntries := make([]model.ClientIpEntry, 0, len(ipTimestamps))
		for ip, timestamp := range ipTimestamps {
			ipsWithTime = append(ipsWithTime, model.ClientIpEntry{IP: ip, Timestamp: timestamp})
			// A live connection's lastSeen may be its old start time.
			// Stamp attribution now so retention cannot evict a connected IP.
			attrTs := timestamp
			if observedAreLive {
				attrTs = now
			}
			attrEntries = append(attrEntries, model.ClientIpEntry{IP: ip, Timestamp: attrTs})
		}
		if len(attrEntries) > 0 {
			attribution[email] = attrEntries
		}

		clientIpsRecord, ok := ipRowByEmail[email]
		if !ok {
			jsonIps, err := model.MarshalClientIpEntries(ipsWithTime)
			if err != nil {
				j.checkError(err)
				continue
			}
			clientIpsRecord = &model.InboundClientIps{ClientEmail: email, Ips: jsonIps}
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "client_email"}},
				DoNothing: true,
			}).Create(clientIpsRecord)
			if result.Error != nil {
				j.checkError(result.Error)
				continue
			}
			if result.RowsAffected > 0 {
				continue
			}
			if err := tx.Where("client_email = ?", email).First(clientIpsRecord).Error; err != nil {
				j.checkError(err)
				continue
			}
		}

		j.updateInboundClientIps(tx, clientIpsRecord, ipsWithTime, observedAreLive)
	}

	if err := tx.Commit().Error; err != nil {
		j.checkError(err)
		return
	}
	committed = true

	j.recordLocalAttribution(attribution)
}

// recordLocalAttribution stores this scan's local observations under this panel's
// own guid so a parent panel can attribute each IP to the node it is on.
// Best-effort: attribution is advisory and must never block IP tracking.
func (j *CheckClientIpJob) recordLocalAttribution(attribution map[string][]model.ClientIpEntry) {
	if len(attribution) == 0 {
		return
	}
	guid, err := (&service.SettingService{}).GetPanelGuid()
	if err != nil || guid == "" {
		return
	}
	if err := (&service.InboundService{}).RecordLocalClientIps(guid, attribution); err != nil {
		logger.Debug("[LimitIP] record local ip attribution failed:", err)
	}
}

// mergeClientIps retains fresh entries and exempts currently observed live
// connections from expiry even when their dispatch timestamps are old.
func mergeClientIps(old, new []model.ClientIpEntry, staleCutoff int64, newAlwaysLive bool) map[string]int64 {
	ipMap := make(map[string]int64, len(old)+len(new))
	for _, ipTime := range old {
		if ipTime.Timestamp < staleCutoff {
			continue
		}
		ipMap[ipTime.IP] = ipTime.Timestamp
	}
	for _, ipTime := range new {
		if !newAlwaysLive && ipTime.Timestamp < staleCutoff {
			continue
		}
		if existingTime, ok := ipMap[ipTime.IP]; !ok || ipTime.Timestamp > existingTime {
			ipMap[ipTime.IP] = ipTime.Timestamp
		}
	}
	return ipMap
}

func (j *CheckClientIpJob) checkError(e error) {
	if e != nil {
		logger.Warning("client ip job err:", e)
	}
}

// delInboundClientIps drops the inbound_client_ips tracking row for an email
// that no longer maps to any inbound (a renamed or deleted client), so stale
// access-log entries don't keep a ghost row alive (#4963).
func (j *CheckClientIpJob) delInboundClientIps(tx *gorm.DB, clientEmail string) {
	if err := tx.Where("client_email = ?", clientEmail).Delete(&model.InboundClientIps{}).Error; err != nil {
		j.checkError(err)
	}
}

// updateInboundClientIps merges this scan's IPs into the tracking row. Node
// sync writes the same blob (#6587), so the write is a compare-and-set; on a
// miss the row is re-read and the merge retried, so neither writer drops the
// other's IPs.
func (j *CheckClientIpJob) updateInboundClientIps(tx *gorm.DB, inboundClientIps *model.InboundClientIps, newIpsWithTime []model.ClientIpEntry, observedAreLive bool) {
	staleCutoff := time.Now().Unix() - ipStaleAfterSeconds

	for range service.ClientIpCasRetries {
		var oldIpsWithTime []model.ClientIpEntry
		if inboundClientIps.Ips != "" {
			_ = json.Unmarshal([]byte(inboundClientIps.Ips), &oldIpsWithTime)
		}
		ipMap := mergeClientIps(oldIpsWithTime, newIpsWithTime, staleCutoff, observedAreLive)

		dbIps := make([]model.ClientIpEntry, 0, len(ipMap))
		for ip, timestamp := range ipMap {
			dbIps = append(dbIps, model.ClientIpEntry{IP: ip, Timestamp: timestamp})
		}
		newIps, err := model.MarshalClientIpEntries(dbIps)
		if err != nil {
			j.checkError(err)
			return
		}
		if newIps == inboundClientIps.Ips {
			return
		}

		updated, err := service.CasUpdateInboundClientIps(tx, inboundClientIps.Id, inboundClientIps.Ips, newIps)
		if err != nil {
			logger.Error("failed to save inboundClientIps:", err)
			return
		}
		if updated {
			inboundClientIps.Ips = newIps
			return
		}
		if err := tx.Where("id = ?", inboundClientIps.Id).First(inboundClientIps).Error; err != nil {
			logger.Error("failed to re-read inboundClientIps after a concurrent write:", err)
			return
		}
	}
	logger.Error("failed to save inboundClientIps: exhausted CAS retries")
}

// getInboundByEmail resolves the inbound that owns a client email. It prefers
// the exact clients/client_inbounds relation; a substring "settings LIKE
// %email%" can match the wrong inbound (an email that is a substring of another,
// or text that merely appears elsewhere in the settings JSON). The LIKE + JSON
// scan stays only as a fallback for clients not yet present in the relation, so
// nothing regresses when the join finds no row.
func (j *CheckClientIpJob) getInboundByEmail(clientEmail string) (*model.Inbound, error) {
	db := database.GetDB()
	inbound := &model.Inbound{}

	err := db.Model(&model.Inbound{}).
		Joins("JOIN client_inbounds ON client_inbounds.inbound_id = inbounds.id").
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email = ?", clientEmail).
		First(inbound).Error
	if err == nil {
		return inbound, nil
	}

	var candidates []model.Inbound
	if listErr := db.Model(&model.Inbound{}).Where("settings LIKE ?", "%"+clientEmail+"%").Find(&candidates).Error; listErr != nil {
		return nil, listErr
	}
	for i := range candidates {
		clients, jsonErr := service.ParseInboundSettingsClients(candidates[i].Settings)
		if jsonErr != nil {
			continue
		}
		for _, client := range clients {
			if client.Email == clientEmail {
				return &candidates[i], nil
			}
		}
	}

	return nil, err
}

// Runs before the apiMode gate: retention must hold for stored rows even
// while nothing is being collected.
func (j *CheckClientIpJob) pruneStaleIpRows() {
	now := time.Now().Unix()
	if now-j.lastIpPrune < ipPruneIntervalSeconds {
		return
	}
	j.lastIpPrune = now
	if err := (&service.InboundService{}).PruneStaleClientIps(); err != nil {
		logger.Warning("prune stale client ip rows failed:", err)
	}
}
