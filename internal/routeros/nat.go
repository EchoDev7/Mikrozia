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
	InInterfaceList string
	Disabled        bool
}

type NATTestResult struct {
	State        string `json:"state"`
	ManagedRules int    `json:"managedRules"`
	Detail       string `json:"detail,omitempty"`
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

	if writeSentence(conn, []string{"/ip/firewall/nat/print", "=.proplist=.id,comment"}) != nil {
		return NATTestResult{State: "unreachable"}
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return NATTestResult{State: state}
	}
	managed := 0
	for _, row := range rows {
		if strings.HasPrefix(row["comment"], ManagedNATCommentPrefix) {
			managed++
		}
	}
	return NATTestResult{State: "connected", ManagedRules: managed}
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
	ifaceList, ok := sanitizeInterfaceList(spec.InInterfaceList)
	if !ok {
		return NATRuleSpec{}, false
	}
	spec.Protocol = proto
	spec.ToAddress = targetIP
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
	if spec.InInterfaceList != "" {
		return row["in-interface-list"] == spec.InInterfaceList
	}
	return row["dst-address-type"] == "local"
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
		"=.proplist=.id,chain,action,protocol,dst-port,to-addresses,to-ports,in-interface-list,dst-address-type,disabled,comment",
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
			if sp.InInterfaceList != "" {
				words = append(words, "=in-interface-list="+sp.InInterfaceList)
			} else {
				words = append(words, "=dst-address-type=local")
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
		if sp.InInterfaceList != "" {
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
