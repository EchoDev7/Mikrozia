package routeros

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	ManagedNATCommentPrefix = "mikrozia:managed:inbound:"
	DefaultAPIPort          = 8728
	maxNATRecords           = 4096
)

type NATConnConfig struct {
	Host     string
	Port     int
	Username string
	Password string
}

type NATRuleSpec struct {
	InboundID       int
	Protocol        string // "tcp" or "udp"
	DstPort         int
	ToAddress       string
	ToPort          int
	InInterface     string
	InInterfaceList string
	Disabled        bool
}

type NATTestResult struct {
	State         string `json:"state"`
	Identity      string `json:"identity,omitempty"`
	Version       string `json:"version,omitempty"`
	BoardName     string `json:"boardName,omitempty"`
	FreeHddSpace  uint64 `json:"freeHddSpace,omitempty"`
	TotalHddSpace uint64 `json:"totalHddSpace,omitempty"`
	HasMasquerade bool   `json:"hasMasquerade"`
	NatRules      int    `json:"natRules"`
	ManagedRules  int    `json:"managedRules"`
	Detail        string `json:"detail,omitempty"`
}

type ManagedNATRule struct {
	ID             string `json:"id"`
	InboundID      int    `json:"inboundId"`
	Protocol       string `json:"protocol"`
	DstPort        string `json:"dstPort"`
	ToAddress      string `json:"toAddress"`
	ToPort         string `json:"toPort"`
	InInterface    string `json:"inInterface,omitempty"`
	DstAddressType string `json:"dstAddressType,omitempty"`
	Disabled       bool   `json:"disabled"`
	Bytes          uint64 `json:"bytes"`
	Packets        uint64 `json:"packets"`
	Comment        string `json:"comment"`
}

type NATReconcileResult struct {
	State      string `json:"state"`
	Added      int    `json:"added"`
	Updated    int    `json:"updated"`
	Removed    int    `json:"removed"`
	RolledBack bool   `json:"rolledBack,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

func ManagedNATComment(inboundID int, protocol string) string {
	return fmt.Sprintf("%s%d:%s", ManagedNATCommentPrefix, inboundID, strings.ToLower(strings.TrimSpace(protocol)))
}

func managedInboundPrefix(inboundID int) string {
	return fmt.Sprintf("%s%d:", ManagedNATCommentPrefix, inboundID)
}

func ValidatePrivateHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	addr, err := netip.ParseAddr(raw)
	if err != nil || addr.Zone() != "" || !addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return "", false
	}
	return addr.String(), true
}

func ValidatePrivateIPv4(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	addr, err := netip.ParseAddr(raw)
	if err != nil || !addr.Is4() || !addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return "", false
	}
	return addr.String(), true
}

func sanitizeInterfaceList(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", true
	}
	if len(s) > 64 {
		return "", false
	}
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.') {
			return "", false
		}
	}
	return s, true
}

func (c Client) dialAndLoginNAT(parent context.Context, cfg NATConnConfig) (net.Conn, *bufio.Reader, string) {
	host, ok := ValidatePrivateHost(cfg.Host)
	if !ok {
		return nil, nil, "invalid_configuration"
	}
	port := cfg.Port
	if port == 0 {
		port = DefaultAPIPort
	}
	if port < 1 || port > 65535 || strings.TrimSpace(cfg.Username) == "" || cfg.Password == "" {
		return nil, nil, "invalid_configuration"
	}
	if parent.Err() != nil {
		return nil, nil, "unreachable"
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		cancel()
		return nil, nil, "unreachable"
	}
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		cancel()
		_ = conn.Close()
		return nil, nil, "unreachable"
	}
	cancel()
	r := bufio.NewReader(conn)
	if writeSentence(conn, []string{"/login", "=name=" + cfg.Username, "=password=" + cfg.Password}) != nil {
		_ = conn.Close()
		return nil, nil, "unreachable"
	}
	state, _, _ := readReply(r, true)
	if state != "ok" {
		_ = conn.Close()
		return nil, nil, state
	}
	return conn, r, "ok"
}

func readNATRecords(r *bufio.Reader) (string, []map[string]string) {
	var rows []map[string]string
	trapped, total := "", 0
	for n := 0; n < maxNATRecords; n++ {
		words, err := readSentence(r)
		if err != nil {
			if errors.Is(err, errReply) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return "invalid_reply", nil
			}
			return "unreachable", nil
		}
		for _, w := range words {
			total += len(w)
		}
		if len(words) == 0 || total > 2*1024*1024 {
			return "invalid_reply", nil
		}
		attrs := map[string]string{}
		for _, w := range words[1:] {
			if !strings.HasPrefix(w, "=") {
				return "invalid_reply", nil
			}
			key, value, ok := strings.Cut(w[1:], "=")
			if !ok || key == "" {
				return "invalid_reply", nil
			}
			attrs[key] = value
		}
		switch words[0] {
		case "!done":
			if trapped != "" {
				return trapped, nil
			}
			return "ok", rows
		case "!re":
			rows = append(rows, attrs)
		case "!empty":
		case "!trap", "!fatal":
			trapped = "query_failed"
			if attrs["category"] == "9" || strings.Contains(strings.ToLower(attrs["message"]), "not enough permissions") {
				trapped = "permission_denied"
			}
			if words[0] == "!fatal" {
				return trapped, nil
			}
		default:
			return "invalid_reply", nil
		}
	}
	return "invalid_reply", nil
}

func readCommandResult(r *bufio.Reader) (string, string, string) {
	trapped, detail, retID := "", "", ""
	for n := 0; n < 32; n++ {
		words, err := readSentence(r)
		if err != nil {
			if errors.Is(err, errReply) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return "invalid_reply", "", ""
			}
			return "unreachable", "", ""
		}
		if len(words) == 0 {
			return "invalid_reply", "", ""
		}
		attrs := map[string]string{}
		for _, w := range words[1:] {
			if !strings.HasPrefix(w, "=") {
				return "invalid_reply", "", ""
			}
			key, value, ok := strings.Cut(w[1:], "=")
			if !ok || key == "" {
				return "invalid_reply", "", ""
			}
			attrs[key] = value
		}
		switch words[0] {
		case "!done":
			if attrs["ret"] != "" {
				retID = attrs["ret"]
			}
			if trapped != "" {
				return trapped, "", detail
			}
			return "ok", retID, ""
		case "!re", "!empty":
		case "!trap", "!fatal":
			trapped = "write_failed"
			detail = attrs["message"]
			if attrs["category"] == "9" || strings.Contains(strings.ToLower(detail), "not enough permissions") {
				trapped = "permission_denied"
			}
			if words[0] == "!fatal" {
				return trapped, "", detail
			}
		default:
			return "invalid_reply", "", ""
		}
	}
	return "invalid_reply", "", ""
}

// TestNATConnection verifies RouterOS API reachability, login, and read access to /ip/firewall/nat.
func (c Client) TestNATConnection(parent context.Context, cfg NATConnConfig) NATTestResult {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return NATTestResult{State: state}
	}
	defer conn.Close()

	if writeSentence(conn, []string{"/ip/firewall/nat/print", "=.proplist=.id,chain,action,disabled,comment"}) != nil {
		return NATTestResult{State: "unreachable"}
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return NATTestResult{State: state}
	}
	managed := 0
	hasMasq := false
	for _, row := range rows {
		if strings.HasPrefix(row["comment"], ManagedNATCommentPrefix) {
			managed++
		}
		rowDisabled := row["disabled"] == "true" || row["disabled"] == "yes"
		if !rowDisabled && row["chain"] == "srcnat" && row["action"] == "masquerade" {
			hasMasq = true
		}
	}
	res := NATTestResult{
		State:         "connected",
		HasMasquerade: hasMasq,
		NatRules:      len(rows),
		ManagedRules:  managed,
	}
	if writeSentence(conn, []string{"/system/identity/print", "=.proplist=name"}) == nil {
		if st, idRows := readNATRecords(r); st == "ok" && len(idRows) > 0 {
			res.Identity = strings.TrimSpace(idRows[0]["name"])
		}
	}
	if writeSentence(conn, []string{"/system/resource/print", "=.proplist=version,board-name,free-hdd-space,total-hdd-space"}) == nil {
		if st, resRows := readNATRecords(r); st == "ok" && len(resRows) > 0 {
			res.Version = strings.TrimSpace(resRows[0]["version"])
			res.BoardName = strings.TrimSpace(resRows[0]["board-name"])
			if v, err := strconv.ParseUint(strings.TrimSpace(resRows[0]["free-hdd-space"]), 10, 64); err == nil {
				res.FreeHddSpace = v
			}
			if v, err := strconv.ParseUint(strings.TrimSpace(resRows[0]["total-hdd-space"]), 10, 64); err == nil {
				res.TotalHddSpace = v
			}
		}
	}
	return res
}

func parseManagedInboundID(comment string) (int, bool) {
	if !strings.HasPrefix(comment, ManagedNATCommentPrefix) {
		return 0, false
	}
	rest := strings.TrimPrefix(comment, ManagedNATCommentPrefix)
	idx := strings.IndexByte(rest, ':')
	if idx <= 0 {
		return 0, false
	}
	id, err := strconv.Atoi(rest[:idx])
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// ListManagedNATRules returns only Mikrozia-owned NAT rules from RouterOS with live byte/packet counters.
func (c Client) ListManagedNATRules(parent context.Context, cfg NATConnConfig) ([]ManagedNATRule, string) {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return nil, state
	}
	defer conn.Close()

	if writeSentence(conn, []string{
		"/ip/firewall/nat/print",
		"=.proplist=.id,chain,action,protocol,dst-port,to-addresses,to-ports,in-interface,dst-address-type,disabled,bytes,packets,comment",
	}) != nil {
		return nil, "unreachable"
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return nil, state
	}
	out := make([]ManagedNATRule, 0)
	for _, row := range rows {
		id, ok := parseManagedInboundID(row["comment"])
		if !ok {
			continue
		}
		bytesVal, _ := strconv.ParseUint(strings.TrimSpace(row["bytes"]), 10, 64)
		pktsVal, _ := strconv.ParseUint(strings.TrimSpace(row["packets"]), 10, 64)
		out = append(out, ManagedNATRule{
			ID:             row[".id"],
			InboundID:      id,
			Protocol:       row["protocol"],
			DstPort:        row["dst-port"],
			ToAddress:      row["to-addresses"],
			ToPort:         row["to-ports"],
			InInterface:    row["in-interface"],
			DstAddressType: row["dst-address-type"],
			Disabled:       row["disabled"] == "true" || row["disabled"] == "yes",
			Bytes:          bytesVal,
			Packets:        pktsVal,
			Comment:        row["comment"],
		})
	}
	return out, "ok"
}

// CleanupOrphanedNAT removes only Mikrozia-owned NAT rules whose inbound ID is not present in validInboundIDs.
func (c Client) CleanupOrphanedNAT(parent context.Context, cfg NATConnConfig, validInboundIDs map[int]bool) (int, string, string) {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return 0, state, ""
	}
	defer conn.Close()

	if writeSentence(conn, []string{"/ip/firewall/nat/print", "=.proplist=.id,comment"}) != nil {
		return 0, "unreachable", ""
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return 0, state, ""
	}
	removed := 0
	for _, row := range rows {
		inboundID, ok := parseManagedInboundID(row["comment"])
		if !ok || validInboundIDs[inboundID] {
			continue
		}
		if writeSentence(conn, []string{"/ip/firewall/nat/remove", "=.id=" + row[".id"]}) != nil {
			return removed, "unreachable", ""
		}
		st, _, detail := readCommandResult(r)
		if st != "ok" {
			return removed, st, detail
		}
		removed++
	}
	return removed, "ok", ""
}

// ExtractWANInterfaceFromGateway parses RouterOS v7 immediate-gw or gateway
// values such as "172.31.0.1%ether1" or "pppoe-out1" and returns a sanitized
// interface name, or "" if no interface name can be extracted.
func ExtractWANInterfaceFromGateway(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.IndexByte(raw, '%'); idx >= 0 && idx+1 < len(raw) {
		cand := strings.TrimSpace(raw[idx+1:])
		if clean, ok := sanitizeInterfaceList(cand); ok {
			return clean
		}
		return ""
	}
	if _, err := netip.ParseAddr(raw); err == nil {
		return ""
	}
	if clean, ok := sanitizeInterfaceList(raw); ok {
		return clean
	}
	return ""
}

// DetectWANInterface queries active default IPv4 routes on RouterOS to infer
// the outgoing WAN interface name (e.g. "ether1", "pppoe-out1", "lte1").
func (c Client) DetectWANInterface(parent context.Context, cfg NATConnConfig) (string, string) {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return "", state
	}
	defer conn.Close()

	if writeSentence(conn, []string{
		"/ip/route/print",
		"?dst-address=0.0.0.0/0",
		"=.proplist=active,immediate-gw,gateway",
	}) != nil {
		return "", "unreachable"
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return "", state
	}
	for _, row := range rows {
		active := row["active"] == "true" || row["active"] == "yes"
		if !active {
			continue
		}
		if iface := ExtractWANInterfaceFromGateway(row["immediate-gw"]); iface != "" {
			return iface, "ok"
		}
		if iface := ExtractWANInterfaceFromGateway(row["gateway"]); iface != "" {
			return iface, "ok"
		}
	}
	return "", "not_found"
}

func validateNATSpec(spec NATRuleSpec) (NATRuleSpec, bool) {
	proto := strings.ToLower(strings.TrimSpace(spec.Protocol))
	if spec.InboundID <= 0 || (proto != "tcp" && proto != "udp") {
		return NATRuleSpec{}, false
	}
	if spec.DstPort < 1 || spec.DstPort > 65535 || spec.ToPort < 1 || spec.ToPort > 65535 {
		return NATRuleSpec{}, false
	}
	targetIP, ok := ValidatePrivateIPv4(spec.ToAddress)
	if !ok {
		return NATRuleSpec{}, false
	}
	iface, ok := sanitizeInterfaceList(spec.InInterface)
	if !ok {
		return NATRuleSpec{}, false
	}
	ifaceList, ok := sanitizeInterfaceList(spec.InInterfaceList)
	if !ok {
		return NATRuleSpec{}, false
	}
	spec.Protocol = proto
	spec.ToAddress = targetIP
	spec.InInterface = iface
	spec.InInterfaceList = ifaceList
	return spec, true
}

func rowMatchesSpec(row map[string]string, spec NATRuleSpec) bool {
	wantDisabled := "false"
	if spec.Disabled {
		wantDisabled = "true"
	}
	rowDisabled := row["disabled"] == "true" || row["disabled"] == "yes"
	wantDisabledBool := wantDisabled == "true"
	if row["chain"] != "dstnat" || row["action"] != "dst-nat" ||
		row["protocol"] != spec.Protocol ||
		row["dst-port"] != strconv.Itoa(spec.DstPort) ||
		row["to-addresses"] != spec.ToAddress ||
		row["to-ports"] != strconv.Itoa(spec.ToPort) ||
		rowDisabled != wantDisabledBool {
		return false
	}
	if spec.InInterface != "" {
		return row["in-interface"] == spec.InInterface
	}
	if spec.InInterfaceList != "" {
		return row["in-interface-list"] == spec.InInterfaceList
	}
	return row["dst-address-type"] == "local" && row["in-interface"] == ""
}

// ReconcileInboundNAT reconciles only Mikrozia-owned NAT rules for inboundID.
// Unmarked or foreign rules are never modified; port conflicts fail closed.
func (c Client) ReconcileInboundNAT(parent context.Context, cfg NATConnConfig, inboundID int, desired []NATRuleSpec) NATReconcileResult {
	if inboundID <= 0 {
		return NATReconcileResult{State: "invalid_configuration", Detail: "invalid inbound id"}
	}
	validated := make([]NATRuleSpec, 0, len(desired))
	for _, sp := range desired {
		sp.InboundID = inboundID
		clean, ok := validateNATSpec(sp)
		if !ok {
			return NATReconcileResult{State: "invalid_configuration", Detail: "invalid NAT rule specification"}
		}
		validated = append(validated, clean)
	}

	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return NATReconcileResult{State: state}
	}
	defer conn.Close()

	if writeSentence(conn, []string{
		"/ip/firewall/nat/print",
		"=.proplist=.id,chain,action,protocol,dst-port,to-addresses,to-ports,in-interface,in-interface-list,dst-address-type,disabled,comment",
	}) != nil {
		return NATReconcileResult{State: "unreachable"}
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return NATReconcileResult{State: state}
	}

	ownPrefix := managedInboundPrefix(inboundID)
	ownedByComment := map[string]map[string]string{}
	for _, row := range rows {
		comment := row["comment"]
		if strings.HasPrefix(comment, ownPrefix) {
			ownedByComment[comment] = row
			continue
		}
		// Conflict detection against any enabled foreign/unmanaged dstnat rule on same proto+port.
		rowDisabled := row["disabled"] == "true" || row["disabled"] == "yes"
		if rowDisabled || row["chain"] != "dstnat" {
			continue
		}
		for _, sp := range validated {
			if sp.Disabled {
				continue
			}
			if strings.EqualFold(row["protocol"], sp.Protocol) && row["dst-port"] == strconv.Itoa(sp.DstPort) {
				return NATReconcileResult{
					State:  "conflict",
					Detail: fmt.Sprintf("existing RouterOS dstnat rule already uses %s/%d", sp.Protocol, sp.DstPort),
				}
			}
		}
	}

	desiredComments := map[string]struct{}{}
	var addedIDs []string
	var res NATReconcileResult

	rollback := func() bool {
		if len(addedIDs) == 0 {
			return false
		}
		for i := len(addedIDs) - 1; i >= 0; i-- {
			_ = writeSentence(conn, []string{"/ip/firewall/nat/remove", "=.id=" + addedIDs[i]})
			_, _, _ = readCommandResult(r)
		}
		return true
	}

	for _, sp := range validated {
		comment := ManagedNATComment(inboundID, sp.Protocol)
		desiredComments[comment] = struct{}{}
		disabledStr := "no"
		if sp.Disabled {
			disabledStr = "yes"
		}
		if existing, ok := ownedByComment[comment]; ok {
			if rowMatchesSpec(existing, sp) {
				continue
			}
			words := []string{
				"/ip/firewall/nat/set",
				"=.id=" + existing[".id"],
				"=chain=dstnat",
				"=action=dst-nat",
				"=protocol=" + sp.Protocol,
				"=dst-port=" + strconv.Itoa(sp.DstPort),
				"=to-addresses=" + sp.ToAddress,
				"=to-ports=" + strconv.Itoa(sp.ToPort),
				"=disabled=" + disabledStr,
				"=comment=" + comment,
			}
			if sp.InInterface != "" {
				words = append(words, "=in-interface="+sp.InInterface, "=!in-interface-list", "=!dst-address-type")
			} else if sp.InInterfaceList != "" {
				words = append(words, "=in-interface-list="+sp.InInterfaceList, "=!in-interface", "=!dst-address-type")
			} else {
				words = append(words, "=dst-address-type=local", "=!in-interface", "=!in-interface-list")
			}
			if writeSentence(conn, words) != nil {
				rb := rollback()
				return NATReconcileResult{State: "unreachable", RolledBack: rb}
			}
			st, _, detail := readCommandResult(r)
			if st != "ok" {
				rb := rollback()
				return NATReconcileResult{State: st, RolledBack: rb, Detail: detail}
			}
			res.Updated++
			continue
		}

		words := []string{
			"/ip/firewall/nat/add",
			"=chain=dstnat",
			"=action=dst-nat",
			"=protocol=" + sp.Protocol,
			"=dst-port=" + strconv.Itoa(sp.DstPort),
			"=to-addresses=" + sp.ToAddress,
			"=to-ports=" + strconv.Itoa(sp.ToPort),
			"=disabled=" + disabledStr,
			"=comment=" + comment,
		}
		if sp.InInterface != "" {
			words = append(words, "=in-interface="+sp.InInterface)
		} else if sp.InInterfaceList != "" {
			words = append(words, "=in-interface-list="+sp.InInterfaceList)
		} else {
			words = append(words, "=dst-address-type=local")
		}
		if writeSentence(conn, words) != nil {
			rb := rollback()
			return NATReconcileResult{State: "unreachable", RolledBack: rb}
		}
		st, retID, detail := readCommandResult(r)
		if st != "ok" {
			rb := rollback()
			return NATReconcileResult{State: st, RolledBack: rb, Detail: detail}
		}
		if retID != "" {
			addedIDs = append(addedIDs, retID)
		}
		res.Added++
	}

	// Remove any stale Mikrozia-owned rules for this inboundID whose protocol is no longer desired.
	for comment, existing := range ownedByComment {
		if _, keep := desiredComments[comment]; keep {
			continue
		}
		if writeSentence(conn, []string{"/ip/firewall/nat/remove", "=.id=" + existing[".id"]}) != nil {
			rb := rollback()
			return NATReconcileResult{State: "unreachable", RolledBack: rb}
		}
		st, _, detail := readCommandResult(r)
		if st != "ok" {
			rb := rollback()
			return NATReconcileResult{State: st, RolledBack: rb, Detail: detail}
		}
		res.Removed++
	}

	res.State = "ok"
	return res
}

// DeleteInboundNAT removes only Mikrozia-owned NAT rules belonging to inboundID.
func (c Client) DeleteInboundNAT(parent context.Context, cfg NATConnConfig, inboundID int) NATReconcileResult {
	return c.ReconcileInboundNAT(parent, cfg, inboundID, nil)
}
