package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/echodev7/mikrozia/v3/internal/database/model"
	"github.com/echodev7/mikrozia/v3/internal/logger"
	"github.com/echodev7/mikrozia/v3/internal/routeros"
	"github.com/echodev7/mikrozia/v3/internal/web/entity"
)

// RouterOSNATService manages opt-in Mikrozia-owned /ip/firewall/nat rules on
// the hosting RouterOS router for local container inbounds.
type RouterOSNATService struct {
	settingService SettingService
	client         routeros.Client
}

// DetectContainerIPv4 returns the first non-loopback private IPv4 address of the container.
func DetectContainerIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if addr, ok := netip.AddrFromSlice(ip.To4()); ok && addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() {
				return addr.String()
			}
		}
	}
	return ""
}

// ProtocolsForInbound determines which RouterOS NAT protocol(s) ("tcp", "udp") an inbound requires.
func ProtocolsForInbound(inbound *model.Inbound) []string {
	if inbound == nil || inbound.Port <= 0 {
		return nil
	}
	// Remote-node inbounds do not listen on this RouterOS container.
	if inbound.NodeID != nil && *inbound.NodeID > 0 {
		return nil
	}
	// Loopback-only listen addresses are intentionally local and not externally exposed.
	listen := strings.TrimSpace(inbound.Listen)
	if listen == "127.0.0.1" || listen == "::1" || strings.EqualFold(listen, "localhost") {
		return nil
	}

	proto := strings.ToLower(strings.TrimSpace(string(inbound.Protocol)))
	switch proto {
	case "amneziawg", "wireguard", "hysteria", "hysteria2", "tuic", "masque":
		return []string{"udp"}
	case "shadowsocks", "dokodemo-door":
		return parseNetworkList(inbound.Settings)
	case "socks", "http":
		return []string{"tcp"}
	}

	// For VLESS / VMess / Trojan / MTProto etc., inspect streamSettings.network.
	var stream struct {
		Network string `json:"network"`
	}
	if strings.TrimSpace(inbound.StreamSettings) != "" {
		_ = json.Unmarshal([]byte(inbound.StreamSettings), &stream)
	}
	switch strings.ToLower(strings.TrimSpace(stream.Network)) {
	case "kcp", "quic", "hysteria":
		return []string{"udp"}
	default:
		return []string{"tcp"}
	}
}

func parseNetworkList(rawSettings string) []string {
	var cfg struct {
		Network string `json:"network"`
	}
	if strings.TrimSpace(rawSettings) != "" {
		_ = json.Unmarshal([]byte(rawSettings), &cfg)
	}
	netStr := strings.ToLower(strings.TrimSpace(cfg.Network))
	if netStr == "" {
		return []string{"tcp"}
	}
	hasTCP := strings.Contains(netStr, "tcp")
	hasUDP := strings.Contains(netStr, "udp")
	var out []string
	if hasTCP {
		out = append(out, "tcp")
	}
	if hasUDP {
		out = append(out, "udp")
	}
	if len(out) == 0 {
		return []string{"tcp"}
	}
	return out
}

func (s *RouterOSNATService) loadEffectiveConfig(override *routeros.NATConnConfig) (*entity.AllSetting, routeros.NATConnConfig, string, error) {
	allSetting, err := s.settingService.GetAllSetting()
	if err != nil {
		return nil, routeros.NATConnConfig{}, "", err
	}
	cfg := routeros.NATConnConfig{
		Host:     strings.TrimSpace(allSetting.RouterosNatHost),
		Port:     allSetting.RouterosNatPort,
		Username: strings.TrimSpace(allSetting.RouterosNatUsername),
		Password: allSetting.RouterosNatPassword,
	}
	if override != nil {
		if strings.TrimSpace(override.Host) != "" {
			cfg.Host = strings.TrimSpace(override.Host)
		}
		if override.Port > 0 {
			cfg.Port = override.Port
		}
		if strings.TrimSpace(override.Username) != "" {
			cfg.Username = strings.TrimSpace(override.Username)
		}
		if override.Password != "" {
			cfg.Password = override.Password
		}
	}
	if cfg.Port <= 0 {
		cfg.Port = routeros.DefaultAPIPort
	}
	targetIP := strings.TrimSpace(allSetting.RouterosNatTargetIP)
	if targetIP == "" {
		targetIP = DetectContainerIPv4()
	}
	return allSetting, cfg, targetIP, nil
}

// TestConnection tests connectivity and read access to /ip/firewall/nat on RouterOS.
func (s *RouterOSNATService) TestConnection(ctx context.Context, override *routeros.NATConnConfig) (routeros.NATTestResult, error) {
	_, cfg, _, err := s.loadEffectiveConfig(override)
	if err != nil {
		return routeros.NATTestResult{State: "invalid_configuration"}, err
	}
	return s.client.TestNATConnection(ctx, cfg), nil
}

// DetectWANInterface detects the active default-route interface name on RouterOS.
func (s *RouterOSNATService) DetectWANInterface(ctx context.Context, override *routeros.NATConnConfig) (string, string, error) {
	_, cfg, _, err := s.loadEffectiveConfig(override)
	if err != nil {
		return "", "invalid_configuration", err
	}
	iface, state := s.client.DetectWANInterface(ctx, cfg)
	return iface, state, nil
}

// ListManagedRules returns live Mikrozia-owned NAT rules from RouterOS.
func (s *RouterOSNATService) ListManagedRules(ctx context.Context) ([]routeros.ManagedNATRule, error) {
	_, cfg, _, err := s.loadEffectiveConfig(nil)
	if err != nil {
		return nil, err
	}
	rules, state := s.client.ListManagedNATRules(ctx, cfg)
	if state != "ok" {
		return nil, fmt.Errorf("failed to read RouterOS NAT rules (%s)", state)
	}
	return rules, nil
}

// CleanupOrphanedRules removes only Mikrozia-owned NAT rules whose inbound no longer exists locally.
func (s *RouterOSNATService) CleanupOrphanedRules(ctx context.Context, inbounds []*model.Inbound) (int, error) {
	allSetting, cfg, _, err := s.loadEffectiveConfig(nil)
	if err != nil {
		return 0, err
	}
	if !allSetting.RouterosNatEnable {
		return 0, fmt.Errorf("RouterOS Managed NAT is disabled")
	}
	valid := make(map[int]bool, len(inbounds))
	for _, ib := range inbounds {
		if ib == nil || (ib.NodeID != nil && *ib.NodeID > 0) {
			continue
		}
		valid[ib.Id] = true
	}
	removed, state, detail := s.client.CleanupOrphanedNAT(ctx, cfg, valid)
	if state != "ok" {
		if detail != "" {
			return removed, fmt.Errorf("RouterOS NAT cleanup failed (%s): %s", state, detail)
		}
		return removed, fmt.Errorf("RouterOS NAT cleanup failed (%s)", state)
	}
	return removed, nil
}

// SyncInbound reconciles the RouterOS NAT rule(s) owned by one inbound when Managed NAT is enabled.
func (s *RouterOSNATService) SyncInbound(ctx context.Context, inbound *model.Inbound) error {
	if inbound == nil || inbound.Id <= 0 {
		return nil
	}
	allSetting, cfg, targetIP, err := s.loadEffectiveConfig(nil)
	if err != nil {
		return err
	}
	if !allSetting.RouterosNatEnable {
		return nil
	}
	protos := ProtocolsForInbound(inbound)
	if len(protos) == 0 {
		// Inbound no longer needs local exposure (e.g. switched to loopback); remove any owned NAT rules.
		res := s.client.DeleteInboundNAT(ctx, cfg, inbound.Id)
		if res.State != "ok" {
			return fmt.Errorf("RouterOS NAT cleanup failed (%s): %s", res.State, res.Detail)
		}
		return nil
	}
	if targetIP == "" {
		return fmt.Errorf("RouterOS NAT target container IP is not configured and could not be auto-detected")
	}

	specs := make([]routeros.NATRuleSpec, 0, len(protos))
	for _, p := range protos {
		specs = append(specs, routeros.NATRuleSpec{
			InboundID:       inbound.Id,
			Protocol:        p,
			DstPort:         inbound.Port,
			ToAddress:       targetIP,
			ToPort:          inbound.Port,
			InInterface:     strings.TrimSpace(allSetting.RouterosNatInInterface),
			InInterfaceList: "",
			Disabled:        !inbound.Enable,
		})
	}
	res := s.client.ReconcileInboundNAT(ctx, cfg, inbound.Id, specs)
	if res.State != "ok" {
		logger.Warningf("RouterOS NAT sync failed for inbound %d: state=%s rolledBack=%v detail=%s", inbound.Id, res.State, res.RolledBack, res.Detail)
		if res.Detail != "" {
			return fmt.Errorf("RouterOS NAT sync failed (%s): %s", res.State, res.Detail)
		}
		return fmt.Errorf("RouterOS NAT sync failed (%s)", res.State)
	}
	return nil
}

// DeleteInbound removes only Mikrozia-owned RouterOS NAT rules for inboundID when Managed NAT is enabled.
func (s *RouterOSNATService) DeleteInbound(ctx context.Context, inboundID int) error {
	if inboundID <= 0 {
		return nil
	}
	allSetting, cfg, _, err := s.loadEffectiveConfig(nil)
	if err != nil {
		return err
	}
	if !allSetting.RouterosNatEnable {
		return nil
	}
	res := s.client.DeleteInboundNAT(ctx, cfg, inboundID)
	if res.State != "ok" {
		logger.Warningf("RouterOS NAT delete failed for inbound %d: state=%s detail=%s", inboundID, res.State, res.Detail)
		if res.Detail != "" {
			return fmt.Errorf("RouterOS NAT delete failed (%s): %s", res.State, res.Detail)
		}
		return fmt.Errorf("RouterOS NAT delete failed (%s)", res.State)
	}
	return nil
}

type RouterOSNATSyncAllResult struct {
	Synced int      `json:"synced"`
	Failed int      `json:"failed"`
	Errors []string `json:"errors,omitempty"`
}

// SyncAllInbounds reconciles Managed RouterOS NAT rules for all provided local inbounds.
func (s *RouterOSNATService) SyncAllInbounds(ctx context.Context, inbounds []*model.Inbound) (RouterOSNATSyncAllResult, error) {
	allSetting, _, _, err := s.loadEffectiveConfig(nil)
	if err != nil {
		return RouterOSNATSyncAllResult{}, err
	}
	if !allSetting.RouterosNatEnable {
		return RouterOSNATSyncAllResult{}, fmt.Errorf("RouterOS Managed NAT is disabled")
	}
	var out RouterOSNATSyncAllResult
	for _, ib := range inbounds {
		if ib == nil || (ib.NodeID != nil && *ib.NodeID > 0) {
			continue
		}
		if err := s.SyncInbound(ctx, ib); err != nil {
			out.Failed++
			out.Errors = append(out.Errors, fmt.Sprintf("inbound %d (%s): %v", ib.Id, ib.Tag, err))
		} else {
			out.Synced++
		}
	}
	return out, nil
}

// JoinErrors joins non-empty error messages into a single error.
func JoinErrors(msgs []string) error {
	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}
