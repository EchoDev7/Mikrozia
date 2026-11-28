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
	ManagedNATCommentPrefix     = "mikrozia:managed:inbound:"
	ManagedServiceCommentPrefix = "mikrozia:managed:service:"
	ManagedACMEHTTP01Comment    = "mikrozia:managed:acme:http01"
	DefaultAPIPort              = 8728
	maxNATRecords               = 4096
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
	Service        string `json:"service,omitempty"`
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

type DiagnosticCheck struct {
	Key    string `json:"key"`
	Status string `json:"status"` // "ok", "warn", "error"
	Detail string `json:"detail"`
}

type DiagnosticsResult struct {
	State  string            `json:"state"`
	Checks []DiagnosticCheck `json:"checks"`
}

type InboundPortCheck struct {
	InboundID int
	Protocol  string
	Port      int
}

func ManagedServiceNATComment(serviceName string) string {
	return ManagedServiceCommentPrefix + strings.ToLower(strings.TrimSpace(serviceName))
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
		comment := row["comment"]
		id, ok := parseManagedInboundID(comment)
		serviceName := ""
		if !ok {
			if strings.HasPrefix(comment, ManagedServiceCommentPrefix) {
				serviceName = strings.TrimPrefix(comment, ManagedServiceCommentPrefix)
				if serviceName == "" {
					continue
				}
			} else {
				continue
			}
		}
		bytesVal, _ := strconv.ParseUint(strings.TrimSpace(row["bytes"]), 10, 64)
		pktsVal, _ := strconv.ParseUint(strings.TrimSpace(row["packets"]), 10, 64)
		out = append(out, ManagedNATRule{
			ID:             row[".id"],
			InboundID:      id,
			Service:        serviceName,
			Protocol:       row["protocol"],
			DstPort:        row["dst-port"],
			ToAddress:      row["to-addresses"],
			ToPort:         row["to-ports"],
			InInterface:    row["in-interface"],
			DstAddressType: row["dst-address-type"],
			Disabled:       row["disabled"] == "true" || row["disabled"] == "yes",
			Bytes:          bytesVal,
			Packets:        pktsVal,
			Comment:        comment,
		})
	}
	return out, "ok"
}

// ReconcileServiceNAT reconciles one Mikrozia-owned service NAT rule (panel or sub)
// marked with comment="mikrozia:managed:service:<name>".
func (c Client) ReconcileServiceNAT(parent context.Context, cfg NATConnConfig, serviceName string, port int, targetIP, inInterface string, disabled bool) NATReconcileResult {
	serviceName = strings.ToLower(strings.TrimSpace(serviceName))
	if serviceName != "panel" && serviceName != "sub" {
		return NATReconcileResult{State: "invalid_configuration", Detail: "unsupported service name"}
	}
	if port < 1 || port > 65535 {
		return NATReconcileResult{State: "invalid_configuration", Detail: "invalid service port"}
	}
	cleanIP, ok := ValidatePrivateIPv4(targetIP)
	if !ok {
		return NATReconcileResult{State: "invalid_configuration", Detail: "invalid private target IP"}
	}
	cleanIface, ok := sanitizeInterfaceList(inInterface)
	if !ok {
		return NATReconcileResult{State: "invalid_configuration", Detail: "invalid in-interface name"}
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

	comment := ManagedServiceNATComment(serviceName)
	var existing map[string]string
	for _, row := range rows {
		if row["comment"] == comment {
			existing = row
			continue
		}
		rowDisabled := row["disabled"] == "true" || row["disabled"] == "yes"
		if !disabled && !rowDisabled && row["chain"] == "dstnat" &&
			strings.EqualFold(row["protocol"], "tcp") && row["dst-port"] == strconv.Itoa(port) {
			return NATReconcileResult{
				State:  "conflict",
				Detail: fmt.Sprintf("existing RouterOS dstnat rule already uses tcp/%d", port),
			}
		}
	}

	spec := NATRuleSpec{
		InboundID:   1,
		Protocol:    "tcp",
		DstPort:     port,
		ToAddress:   cleanIP,
		ToPort:      port,
		InInterface: cleanIface,
		Disabled:    disabled,
	}
	disabledStr := "no"
	if disabled {
		disabledStr = "yes"
	}

	if existing != nil {
		if rowMatchesSpec(existing, spec) {
			return NATReconcileResult{State: "ok"}
		}
		words := []string{
			"/ip/firewall/nat/set",
			"=.id=" + existing[".id"],
			"=chain=dstnat",
			"=action=dst-nat",
			"=protocol=tcp",
			"=dst-port=" + strconv.Itoa(port),
			"=to-addresses=" + cleanIP,
			"=to-ports=" + strconv.Itoa(port),
			"=disabled=" + disabledStr,
			"=comment=" + comment,
		}
		if cleanIface != "" {
			words = append(words, "=in-interface="+cleanIface, "=!in-interface-list", "=!dst-address-type")
		} else {
			words = append(words, "=dst-address-type=local", "=!in-interface", "=!in-interface-list")
		}
		if writeSentence(conn, words) != nil {
			return NATReconcileResult{State: "unreachable"}
		}
		st, _, detail := readCommandResult(r)
		if st != "ok" {
			return NATReconcileResult{State: st, Detail: detail}
		}
		return NATReconcileResult{State: "ok", Updated: 1}
	}

	words := []string{
		"/ip/firewall/nat/add",
		"=chain=dstnat",
		"=action=dst-nat",
		"=protocol=tcp",
		"=dst-port=" + strconv.Itoa(port),
		"=to-addresses=" + cleanIP,
		"=to-ports=" + strconv.Itoa(port),
		"=disabled=" + disabledStr,
		"=comment=" + comment,
	}
	if cleanIface != "" {
		words = append(words, "=in-interface="+cleanIface)
	} else {
		words = append(words, "=dst-address-type=local")
	}
	if writeSentence(conn, words) != nil {
		return NATReconcileResult{State: "unreachable"}
	}
	st, _, detail := readCommandResult(r)
	if st != "ok" {
		return NATReconcileResult{State: st, Detail: detail}
	}
	return NATReconcileResult{State: "ok", Added: 1}
}

// RunDiagnostics performs read-only RouterOS pre-flight checks:
// free disk space, VETH interface status, container gateway IP, masquerade NAT, and inbound port conflicts.
func (c Client) RunDiagnostics(parent context.Context, cfg NATConnConfig, targetIP string, inbounds []InboundPortCheck) DiagnosticsResult {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return DiagnosticsResult{State: state}
	}
	defer conn.Close()

	checks := make([]DiagnosticCheck, 0, 5)

	// 1. RouterOS resource & free disk space
	if writeSentence(conn, []string{"/system/resource/print", "=.proplist=version,board-name,free-hdd-space,total-hdd-space"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" && len(rows) > 0 {
			ver := strings.TrimSpace(rows[0]["version"])
			board := strings.TrimSpace(rows[0]["board-name"])
			freeBytes, _ := strconv.ParseUint(strings.TrimSpace(rows[0]["free-hdd-space"]), 10, 64)
			totalBytes, _ := strconv.ParseUint(strings.TrimSpace(rows[0]["total-hdd-space"]), 10, 64)
			freeMB := freeBytes / (1024 * 1024)
			totalMB := totalBytes / (1024 * 1024)
			status := "ok"
			if freeBytes > 0 && freeMB < 64 {
				status = "error"
			} else if freeBytes > 0 && freeMB < 256 {
				status = "warn"
			}
			checks = append(checks, DiagnosticCheck{
				Key:    "disk",
				Status: status,
				Detail: fmt.Sprintf("RouterOS %s (%s) · Free disk: %d MB / %d MB", ver, board, freeMB, totalMB),
			})
		}
	}

	// 2. VETH interface running check
	if writeSentence(conn, []string{"/interface/print", "=.proplist=name,type,running,disabled"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			foundName := ""
			running := false
			for _, row := range rows {
				if row["type"] == "veth" || strings.Contains(strings.ToLower(row["name"]), "veth") {
					foundName = row["name"]
					if (row["running"] == "true" || row["running"] == "yes") &&
						row["disabled"] != "true" && row["disabled"] != "yes" {
						running = true
						break
					}
				}
			}
			switch {
			case running:
				checks = append(checks, DiagnosticCheck{Key: "veth", Status: "ok", Detail: fmt.Sprintf("%s is up and running", foundName)})
			case foundName != "":
				checks = append(checks, DiagnosticCheck{Key: "veth", Status: "error", Detail: fmt.Sprintf("%s exists but is not running", foundName)})
			default:
				checks = append(checks, DiagnosticCheck{Key: "veth", Status: "warn", Detail: "No VETH interface found on RouterOS"})
			}
		}
	}

	// 3. Container gateway IP check
	if writeSentence(conn, []string{"/ip/address/print", "=.proplist=address,interface,disabled"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			targetPrefix := ""
			if parts := strings.Split(strings.TrimSpace(targetIP), "."); len(parts) == 4 {
				targetPrefix = parts[0] + "." + parts[1] + "." + parts[2] + "."
			}
			matchedAddr := ""
			matchedIface := ""
			for _, row := range rows {
				if row["disabled"] == "true" || row["disabled"] == "yes" {
					continue
				}
				addr := strings.TrimSpace(row["address"])
				if targetPrefix != "" && strings.HasPrefix(addr, targetPrefix) {
					matchedAddr = addr
					matchedIface = row["interface"]
					break
				}
			}
			if matchedAddr != "" {
				checks = append(checks, DiagnosticCheck{Key: "gateway", Status: "ok", Detail: fmt.Sprintf("%s on %s", matchedAddr, matchedIface)})
			} else {
				checks = append(checks, DiagnosticCheck{Key: "gateway", Status: "warn", Detail: fmt.Sprintf("No active gateway IP matching container target %s", targetIP)})
			}
		}
	}

	// 4 & 5. Masquerade check + inbound port conflict check
	if writeSentence(conn, []string{"/ip/firewall/nat/print", "=.proplist=.id,chain,action,protocol,dst-port,disabled,comment"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			hasMasq := false
			var conflicts []string
			for _, row := range rows {
				rowDisabled := row["disabled"] == "true" || row["disabled"] == "yes"
				if rowDisabled {
					continue
				}
				if row["chain"] == "srcnat" && row["action"] == "masquerade" {
					hasMasq = true
				}
				if row["chain"] == "dstnat" &&
					!strings.HasPrefix(row["comment"], ManagedNATCommentPrefix) &&
					!strings.HasPrefix(row["comment"], ManagedServiceCommentPrefix) {
					for _, ib := range inbounds {
						if strings.EqualFold(row["protocol"], ib.Protocol) && row["dst-port"] == strconv.Itoa(ib.Port) {
							conflicts = append(conflicts, fmt.Sprintf("#%d (%s/%d)", ib.InboundID, ib.Protocol, ib.Port))
						}
					}
				}
			}
			if hasMasq {
				checks = append(checks, DiagnosticCheck{Key: "masquerade", Status: "ok", Detail: "Active srcnat masquerade rule found"})
			} else {
				checks = append(checks, DiagnosticCheck{Key: "masquerade", Status: "error", Detail: "No active srcnat masquerade rule found; container may lack outbound internet"})
			}
			if len(conflicts) == 0 {
				checks = append(checks, DiagnosticCheck{Key: "conflicts", Status: "ok", Detail: "No port conflicts with manual RouterOS dstnat rules"})
			} else {
				checks = append(checks, DiagnosticCheck{Key: "conflicts", Status: "warn", Detail: "Manual dstnat conflict on: " + strings.Join(conflicts, ", ")})
			}
		}
	}

	// 6. RouterOS built-in /ip/service conflict check (www:80, www-ssl:443, or inbound ports)
	if writeSentence(conn, []string{"/ip/service/print", "=.proplist=name,port,disabled,dynamic"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			inboundPorts := map[int]bool{80: true, 443: true}
			for _, ib := range inbounds {
				if ib.Port > 0 {
					inboundPorts[ib.Port] = true
				}
			}
			var svcConflicts []string
			for _, row := range rows {
				if row["disabled"] == "true" || row["disabled"] == "yes" {
					continue
				}
				if row["dynamic"] == "true" || row["dynamic"] == "yes" {
					continue
				}
				name := strings.TrimSpace(row["name"])
				lowerName := strings.ToLower(name)
				if strings.HasPrefix(lowerName, "xray") || lowerName == "x-ui" {
					continue
				}
				portNum, err := strconv.Atoi(strings.TrimSpace(row["port"]))
				if err != nil || !inboundPorts[portNum] {
					continue
				}
				if name == "" {
					name = "service"
				}
				svcConflicts = append(svcConflicts, fmt.Sprintf("%s:%d", name, portNum))
			}
			if len(svcConflicts) == 0 {
				checks = append(checks, DiagnosticCheck{Key: "services", Status: "ok", Detail: "No conflicting RouterOS /ip/service listeners on port 80, 443, or inbound ports"})
			} else {
				checks = append(checks, DiagnosticCheck{Key: "services", Status: "warn", Detail: "Active RouterOS service(s) may conflict: " + strings.Join(svcConflicts, ", ")})
			}
		}
	}

	// 7. Connection tracking table usage (/ip/firewall/connection/tracking/print)
	if writeSentence(conn, []string{"/ip/firewall/connection/tracking/print", "=.proplist=enabled,total-entries,max-entries"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" && len(rows) > 0 {
			row := rows[0]
			enabled := strings.ToLower(strings.TrimSpace(row["enabled"]))
			if enabled == "no" || enabled == "false" {
				checks = append(checks, DiagnosticCheck{Key: "conntrack", Status: "warn", Detail: "Connection tracking is disabled on RouterOS"})
			} else {
				total, errT := strconv.ParseUint(strings.TrimSpace(row["total-entries"]), 10, 64)
				maxVal, errM := strconv.ParseUint(strings.TrimSpace(row["max-entries"]), 10, 64)
				if errT == nil && errM == nil && maxVal > 0 {
					pct := float64(total) * 100.0 / float64(maxVal)
					detail := fmt.Sprintf("%d / %d entries (%.1f%%)", total, maxVal, pct)
					switch {
					case pct >= 90:
						checks = append(checks, DiagnosticCheck{Key: "conntrack", Status: "error", Detail: detail})
					case pct >= 75:
						checks = append(checks, DiagnosticCheck{Key: "conntrack", Status: "warn", Detail: detail})
					default:
						checks = append(checks, DiagnosticCheck{Key: "conntrack", Status: "ok", Detail: detail})
					}
				} else {
					checks = append(checks, DiagnosticCheck{Key: "conntrack", Status: "ok", Detail: "Connection tracking active"})
				}
			}
		}
	}

	// 8. RouterOS hardware health sensors (/system/health/print)
	if writeSentence(conn, []string{"/system/health/print", "=.proplist=name,value,type"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			if len(rows) == 0 {
				checks = append(checks, DiagnosticCheck{Key: "hardware", Status: "ok", Detail: "No hardware sensors reported (normal on CHR / virtualized RouterOS)"})
			} else {
				var parts []string
				hwStatus := "ok"
				for _, row := range rows {
					name := strings.TrimSpace(row["name"])
					val := strings.TrimSpace(row["value"])
					unit := strings.TrimSpace(row["type"])
					if name == "" || val == "" {
						continue
					}
					parts = append(parts, fmt.Sprintf("%s: %s%s", name, val, unit))
					if strings.Contains(strings.ToLower(name), "temp") {
						if temp, err := strconv.ParseFloat(val, 64); err == nil {
							if temp >= 80 {
								hwStatus = "error"
							} else if temp >= 70 && hwStatus != "error" {
								hwStatus = "warn"
							}
						}
					}
				}
				if len(parts) == 0 {
					checks = append(checks, DiagnosticCheck{Key: "hardware", Status: "ok", Detail: "No hardware sensors reported (normal on CHR / virtualized RouterOS)"})
				} else {
					checks = append(checks, DiagnosticCheck{Key: "hardware", Status: hwStatus, Detail: strings.Join(parts, " · ")})
				}
			}
		}
	}

	return DiagnosticsResult{State: "ok", Checks: checks}
}

type Port80Snapshot struct {
	Port80Status    string
	ServiceConflict string
}

func dstPortIncludes(raw string, target int) bool {
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if loStr, hiStr, ok := strings.Cut(part, "-"); ok {
			lo, err1 := strconv.Atoi(strings.TrimSpace(loStr))
			hi, err2 := strconv.Atoi(strings.TrimSpace(hiStr))
			if err1 == nil && err2 == nil && target >= lo && target <= hi {
				return true
			}
			continue
		}
		if p, err := strconv.Atoi(part); err == nil && p == target {
			return true
		}
	}
	return false
}

// CheckPort80AndServices inspects RouterOS /ip/firewall/nat and /ip/service in
// one read-only session to report port 80 dst-nat state and whether built-in
// RouterOS services (such as www:80 or www-ssl:443) are enabled on ports 80/443.
func (c Client) CheckPort80AndServices(parent context.Context, cfg NATConnConfig) Port80Snapshot {
	out := Port80Snapshot{Port80Status: "unknown"}
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return out
	}
	defer conn.Close()

	if writeSentence(conn, []string{
		"/ip/firewall/nat/print",
		"=.proplist=chain,action,protocol,dst-port,disabled",
	}) != nil {
		return out
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return out
	}
	out.Port80Status = "closed"
	for _, row := range rows {
		if row["disabled"] == "true" || row["disabled"] == "yes" {
			continue
		}
		if row["chain"] != "dstnat" || row["action"] != "dst-nat" {
			continue
		}
		proto := strings.ToLower(strings.TrimSpace(row["protocol"]))
		if proto != "" && proto != "tcp" {
			continue
		}
		if dstPortIncludes(row["dst-port"], 80) {
			out.Port80Status = "open"
			break
		}
	}

	if writeSentence(conn, []string{
		"/ip/service/print",
		"=.proplist=name,port,disabled,dynamic",
	}) == nil {
		if st, svcRows := readNATRecords(r); st == "ok" {
			var conflicts []string
			for _, row := range svcRows {
				if row["disabled"] == "true" || row["disabled"] == "yes" {
					continue
				}
				if row["dynamic"] == "true" || row["dynamic"] == "yes" {
					continue
				}
				name := strings.TrimSpace(row["name"])
				lowerName := strings.ToLower(name)
				if strings.HasPrefix(lowerName, "xray") || lowerName == "x-ui" {
					continue
				}
				portNum, err := strconv.Atoi(strings.TrimSpace(row["port"]))
				if err != nil || (portNum != 80 && portNum != 443) {
					continue
				}
				if name == "" {
					name = "service"
				}
				conflicts = append(conflicts, fmt.Sprintf("%s:%d", name, portNum))
			}
			out.ServiceConflict = strings.Join(conflicts, ", ")
		}
	}
	return out
}

// CheckPort80Status inspects RouterOS /ip/firewall/nat in read-only mode to
// report whether an enabled TCP dst-nat rule currently forwards port 80.
// Returns "open", "closed", or "unknown".
func (c Client) CheckPort80Status(parent context.Context, cfg NATConnConfig) string {
	return c.CheckPort80AndServices(parent, cfg).Port80Status
}

// InterfaceTrafficSnapshot holds live traffic rates and cumulative counters read
// directly from RouterOS for the container's VETH interface.
// Note: from the router's perspective on the veth pair, router RX = container Upload (Sent)
// and router TX = container Download (Received).
type InterfaceTrafficSnapshot struct {
	OK          bool
	Interface   string
	UpBytesPS   uint64 // container upload (router rx-bits-per-second / 8)
	DownBytesPS uint64 // container download (router tx-bits-per-second / 8)
	PktUpPS     uint64
	PktDownPS   uint64
	SentBytes   uint64 // router rx-byte
	RecvBytes   uint64 // router tx-byte
	PktSent     uint64 // router rx-packet
	PktRecv     uint64 // router tx-packet
}

// ReadInterfaceTraffic queries RouterOS /interface/monitor-traffic (once) and
// /interface/print stats for the container VETH interface so Overall Speed matches
// MikroTik WinBox/WebFig exactly.
func (c Client) ReadInterfaceTraffic(parent context.Context, cfg NATConnConfig, ifaceName, containerComment string) InterfaceTrafficSnapshot {
	out := InterfaceTrafficSnapshot{Interface: strings.TrimSpace(ifaceName)}
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return out
	}
	defer conn.Close()

	// Discover the container's veth interface name once if not yet cached.
	if out.Interface == "" {
		if writeSentence(conn, []string{
			"/container/print",
			"=.proplist=interface,comment,status,running,tag,remote-image,name",
		}) == nil {
			if st, rows := readNATRecords(r); st == "ok" {
				commentTarget := strings.TrimSpace(containerComment)
				if commentTarget == "" {
					commentTarget = "[Mikrozia:container] Container"
				}
				for _, row := range rows {
					if strings.TrimSpace(row["comment"]) == commentTarget && strings.TrimSpace(row["interface"]) != "" {
						out.Interface = strings.TrimSpace(row["interface"])
						break
					}
				}
				if out.Interface == "" {
					for _, row := range rows {
						if (strings.EqualFold(row["status"], "running") || routerOSBool(row["running"])) &&
							strings.Contains(strings.ToLower(row["tag"]+row["remote-image"]+row["name"]), "mikrozia") &&
							strings.TrimSpace(row["interface"]) != "" {
							out.Interface = strings.TrimSpace(row["interface"])
							break
						}
					}
				}
			}
		}
	}
	if out.Interface == "" {
		return out
	}

	// 1. Live 1-second instantaneous rates from /interface/monitor-traffic once
	if writeSentence(conn, []string{
		"/interface/monitor-traffic",
		"=interface=" + out.Interface,
		"=once=",
	}) != nil {
		return out
	}
	if st, rows := readNATRecords(r); st == "ok" && len(rows) > 0 {
		row := rows[0]
		rxBps, _ := strconv.ParseUint(strings.TrimSpace(row["rx-bits-per-second"]), 10, 64)
		txBps, _ := strconv.ParseUint(strings.TrimSpace(row["tx-bits-per-second"]), 10, 64)
		rxPps, _ := strconv.ParseUint(strings.TrimSpace(row["rx-packets-per-second"]), 10, 64)
		txPps, _ := strconv.ParseUint(strings.TrimSpace(row["tx-packets-per-second"]), 10, 64)
		out.UpBytesPS = rxBps / 8
		out.DownBytesPS = txBps / 8
		out.PktUpPS = rxPps
		out.PktDownPS = txPps
		out.OK = true
	}

	// 2. Cumulative persistent byte/packet totals from /interface/print
	if writeSentence(conn, []string{
		"/interface/print",
		"?name=" + out.Interface,
		"=.proplist=rx-byte,tx-byte,rx-packet,tx-packet",
	}) == nil {
		if st, rows := readNATRecords(r); st == "ok" && len(rows) > 0 {
			row := rows[0]
			out.SentBytes, _ = strconv.ParseUint(strings.TrimSpace(row["rx-byte"]), 10, 64)
			out.RecvBytes, _ = strconv.ParseUint(strings.TrimSpace(row["tx-byte"]), 10, 64)
			out.PktSent, _ = strconv.ParseUint(strings.TrimSpace(row["rx-packet"]), 10, 64)
			out.PktRecv, _ = strconv.ParseUint(strings.TrimSpace(row["tx-packet"]), 10, 64)
		}
	}

	return out
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

// WANNetworkSummary holds the active RouterOS default-route gateway and WAN interface.
type WANNetworkSummary struct {
	Gateway   string
	Interface string
}

// ReadWANNetworkSummary queries RouterOS active default IPv4 routes to return
// both the router's upstream gateway address and its WAN interface name.
func (c Client) ReadWANNetworkSummary(parent context.Context, cfg NATConnConfig) WANNetworkSummary {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return WANNetworkSummary{}
	}
	defer conn.Close()

	if writeSentence(conn, []string{
		"/ip/route/print",
		"?dst-address=0.0.0.0/0",
		"=.proplist=active,immediate-gw,gateway",
	}) != nil {
		return WANNetworkSummary{}
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return WANNetworkSummary{}
	}
	for _, row := range rows {
		active := row["active"] == "true" || row["active"] == "yes"
		if !active {
			continue
		}
		rawGW := strings.TrimSpace(row["immediate-gw"])
		if rawGW == "" {
			rawGW = strings.TrimSpace(row["gateway"])
		}
		gwAddr := rawGW
		if before, _, ok := strings.Cut(rawGW, "%"); ok {
			gwAddr = strings.TrimSpace(before)
		}
		iface := ExtractWANInterfaceFromGateway(row["immediate-gw"])
		if iface == "" {
			iface = ExtractWANInterfaceFromGateway(row["gateway"])
		}
		return WANNetworkSummary{Gateway: gwAddr, Interface: iface}
	}
	return WANNetworkSummary{}
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

func portListContains(raw string, port int) bool {
	target := strconv.Itoa(port)
	for _, token := range strings.Split(raw, ",") {
		part := strings.TrimSpace(token)
		if part == "" {
			continue
		}
		if part == target {
			return true
		}
		if dash := strings.IndexByte(part, '-'); dash > 0 {
			lo, err1 := strconv.Atoi(strings.TrimSpace(part[:dash]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(part[dash+1:]))
			if err1 == nil && err2 == nil && port >= lo && port <= hi {
				return true
			}
		}
	}
	return false
}

// OpenTemporaryACMEPort80 creates one temporary Mikrozia-owned dst-nat rule for
// TCP port 80 -> targetIP:80 marked with ManagedACMEHTTP01Comment. It refuses to
// proceed if an enabled RouterOS /ip/service (such as www) or an unmanaged dstnat
// rule already occupies port 80.
func (c Client) OpenTemporaryACMEPort80(parent context.Context, cfg NATConnConfig, targetIP, inInterface string) (bool, string, string) {
	cleanIP, ok := ValidatePrivateIPv4(targetIP)
	if !ok {
		return false, "invalid_configuration", "invalid private container target IP"
	}
	cleanIface, ok := sanitizeInterfaceList(inInterface)
	if !ok {
		return false, "invalid_configuration", "invalid in-interface name"
	}

	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return false, state, ""
	}
	defer conn.Close()

	// Pre-flight 1: ensure no enabled static RouterOS /ip/service holds port 80.
	if writeSentence(conn, []string{
		"/ip/service/print",
		"=.proplist=name,port,disabled,dynamic",
	}) != nil {
		return false, "unreachable", ""
	}
	st, svcRows := readNATRecords(r)
	if st != "ok" {
		return false, st, ""
	}
	for _, row := range svcRows {
		if row["disabled"] == "true" || row["disabled"] == "yes" {
			continue
		}
		if row["dynamic"] == "true" || row["dynamic"] == "yes" {
			continue
		}
		name := strings.TrimSpace(row["name"])
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "xray") || strings.HasPrefix(lower, "x-ui") {
			continue
		}
		if strings.TrimSpace(row["port"]) == "80" {
			return false, "conflict", fmt.Sprintf("RouterOS service %q is enabled on port 80; disable or move it first", name)
		}
	}

	// Pre-flight 2: inspect existing /ip/firewall/nat rules on port 80.
	if writeSentence(conn, []string{
		"/ip/firewall/nat/print",
		"=.proplist=.id,chain,action,protocol,dst-port,to-addresses,to-ports,disabled,comment",
	}) != nil {
		return false, "unreachable", ""
	}
	st, natRows := readNATRecords(r)
	if st != "ok" {
		return false, st, ""
	}
	var staleOwnedIDs []string
	alreadyActive := false
	for _, row := range natRows {
		comment := strings.TrimSpace(row["comment"])
		disabled := row["disabled"] == "true" || row["disabled"] == "yes"
		if comment == ManagedACMEHTTP01Comment {
			if !disabled &&
				strings.EqualFold(row["chain"], "dstnat") &&
				strings.EqualFold(row["action"], "dst-nat") &&
				strings.EqualFold(row["protocol"], "tcp") &&
				strings.TrimSpace(row["dst-port"]) == "80" &&
				strings.TrimSpace(row["to-addresses"]) == cleanIP &&
				strings.TrimSpace(row["to-ports"]) == "80" {
				alreadyActive = true
				continue
			}
			if id := strings.TrimSpace(row[".id"]); id != "" {
				staleOwnedIDs = append(staleOwnedIDs, id)
			}
			continue
		}
		if disabled || !strings.EqualFold(row["chain"], "dstnat") {
			continue
		}
		if strings.EqualFold(row["protocol"], "tcp") && portListContains(row["dst-port"], 80) {
			return false, "conflict", "an existing unmanaged RouterOS NAT rule already forwards port 80"
		}
	}

	for _, id := range staleOwnedIDs {
		if writeSentence(conn, []string{"/ip/firewall/nat/remove", "=.id=" + id}) != nil {
			return false, "unreachable", ""
		}
		st, _, detail := readCommandResult(r)
		if st != "ok" {
			return false, st, detail
		}
	}

	if alreadyActive {
		return true, "ok", ""
	}

	words := []string{
		"/ip/firewall/nat/add",
		"=chain=dstnat",
		"=action=dst-nat",
		"=protocol=tcp",
		"=dst-port=80",
		"=to-addresses=" + cleanIP,
		"=to-ports=80",
		"=comment=" + ManagedACMEHTTP01Comment,
	}
	if cleanIface != "" {
		words = append(words, "=in-interface="+cleanIface)
	} else {
		words = append(words, "=dst-address-type=local")
	}
	if writeSentence(conn, words) != nil {
		return false, "unreachable", ""
	}
	st, _, detail := readCommandResult(r)
	if st != "ok" {
		return false, st, detail
	}
	return true, "ok", ""
}

// CloseTemporaryACMEPort80 removes only Mikrozia-owned temporary ACME HTTP-01
// NAT rules marked with ManagedACMEHTTP01Comment.
func (c Client) CloseTemporaryACMEPort80(parent context.Context, cfg NATConnConfig) (int, string, string) {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return 0, state, ""
	}
	defer conn.Close()

	if writeSentence(conn, []string{
		"/ip/firewall/nat/print",
		"=.proplist=.id,comment",
	}) != nil {
		return 0, "unreachable", ""
	}
	st, rows := readNATRecords(r)
	if st != "ok" {
		return 0, st, ""
	}
	removed := 0
	for _, row := range rows {
		if strings.TrimSpace(row["comment"]) != ManagedACMEHTTP01Comment {
			continue
		}
		id := strings.TrimSpace(row[".id"])
		if id == "" {
			continue
		}
		if writeSentence(conn, []string{"/ip/firewall/nat/remove", "=.id=" + id}) != nil {
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
