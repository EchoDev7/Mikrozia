package routeros

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const maxWord = 64 * 1024

var errReply = errors.New("invalid RouterOS API reply")

// ResourceSnapshot holds the parsed read-only fields from /system/resource/print.
type ResourceSnapshot struct {
	State            string
	UptimeSeconds    *int64
	CPULoad          *int
	CPUCount         *int
	CPUFrequencyMhz  *int
	MemUsed          *uint64
	MemTotal         *uint64
	Version          string
	BoardName        string
	ArchitectureName string
}

// Client deliberately exposes no arbitrary command execution or configuration operations.
type Client struct {
	Dial func(context.Context, string, string) (net.Conn, error)
}

// Health returns only allowlisted states; endpoint and peer-supplied error text never escape.
func (c Client) Health(parent context.Context, host, username, password string) string {
	return c.ResourceSnapshot(parent, host, username, password).State
}

// ResourceSnapshot executes one read-only /system/resource/print query over TCP/8728
// and strictly validates uptime, cpu-load, cpu-count, and cpu-frequency.
func (c Client) ResourceSnapshot(parent context.Context, host, username, password string) ResourceSnapshot {
	if parent.Err() != nil {
		return ResourceSnapshot{State: "unreachable"}
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, "8728"))
	if err != nil {
		return ResourceSnapshot{State: "unreachable"}
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return ResourceSnapshot{State: "unreachable"}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	r := bufio.NewReader(conn)
	if writeSentence(conn, []string{"/login", "=name=" + username, "=password=" + password}) != nil {
		return ResourceSnapshot{State: "unreachable"}
	}
	state, _, _ := readReply(r, true)
	if state != "ok" {
		return ResourceSnapshot{State: state}
	}
	if writeSentence(conn, []string{"/system/resource/print", "=.proplist=uptime,cpu-load,cpu-count,cpu-frequency,total-memory,free-memory,version,board-name,architecture-name"}) != nil {
		return ResourceSnapshot{State: "unreachable"}
	}
	state, records, attrs := readReply(r, false)
	if state != "ok" {
		return ResourceSnapshot{State: state}
	}
	if records != 1 {
		return ResourceSnapshot{State: "invalid_reply"}
	}
	loadVal, ok := parseBoundedInt(attrs["cpu-load"], 0, 100)
	if !ok {
		return ResourceSnapshot{State: "invalid_reply"}
	}
	snap := ResourceSnapshot{
		State:            "connected",
		CPULoad:          &loadVal,
		Version:          strings.TrimSpace(attrs["version"]),
		BoardName:        strings.TrimSpace(attrs["board-name"]),
		ArchitectureName: strings.TrimSpace(attrs["architecture-name"]),
	}
	if upSec, ok := parseRouterOSUptime(attrs["uptime"]); ok {
		snap.UptimeSeconds = &upSec
	}
	if cnt, ok := parsePositiveInt(attrs["cpu-count"]); ok {
		snap.CPUCount = &cnt
	}
	if freq, ok := parsePositiveInt(attrs["cpu-frequency"]); ok {
		snap.CPUFrequencyMhz = &freq
	}
	if used, total, ok := parseRouterOSMemory(attrs["total-memory"], attrs["free-memory"]); ok {
		snap.MemUsed = &used
		snap.MemTotal = &total
	}
	return snap
}

func parseRouterOSMemory(rawTotal, rawFree string) (uint64, uint64, bool) {
	if rawTotal == "" || rawFree == "" {
		return 0, 0, false
	}
	total, err := strconv.ParseUint(strings.TrimSpace(rawTotal), 10, 64)
	if err != nil || total == 0 {
		return 0, 0, false
	}
	free, err := strconv.ParseUint(strings.TrimSpace(rawFree), 10, 64)
	if err != nil || free > total {
		return 0, 0, false
	}
	return total - free, total, true
}

func parseBoundedInt(raw string, minVal, maxVal int) (int, bool) {
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < minVal || v > maxVal {
		return 0, false
	}
	return v, true
}

func parsePositiveInt(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

func parseRouterOSUptime(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, false
	}
	var total int64
	var num int64
	hasDigit := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= '0' && ch <= '9' {
			num = num*10 + int64(ch-'0')
			hasDigit = true
			continue
		}
		if !hasDigit {
			return 0, false
		}
		switch ch {
		case 'w':
			total += num * 7 * 86400
		case 'd':
			total += num * 86400
		case 'h':
			total += num * 3600
		case 'm':
			total += num * 60
		case 's':
			total += num
		default:
			return 0, false
		}
		num = 0
		hasDigit = false
	}
	if hasDigit {
		return 0, false
	}
	return total, true
}

func readReply(r *bufio.Reader, login bool) (string, int, map[string]string) {
	records, trapped, total := 0, "", 0
	var lastAttrs map[string]string
	for n := 0; n < 32; n++ {
		words, err := readSentence(r)
		if err != nil {
			if errors.Is(err, errReply) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return "invalid_reply", records, nil
			}
			return "unreachable", records, nil
		}
		for _, w := range words {
			total += len(w)
		}
		if len(words) == 0 || total > 128*1024 {
			return "invalid_reply", records, nil
		}
		attrs := map[string]string{}
		for _, w := range words[1:] {
			if !strings.HasPrefix(w, "=") {
				return "invalid_reply", records, nil
			}
			key, value, ok := strings.Cut(w[1:], "=")
			if !ok || key == "" {
				return "invalid_reply", records, nil
			}
			attrs[key] = value
		}
		switch words[0] {
		case "!done":
			if login && attrs["ret"] != "" {
				return "authentication_failed", records, nil
			}
			if trapped != "" {
				return trapped, records, nil
			}
			return "ok", records, lastAttrs
		case "!re":
			if login || attrs["uptime"] == "" {
				return "invalid_reply", records, nil
			}
			lastAttrs = attrs
			records++
		case "!empty":
			if login {
				return "invalid_reply", records, nil
			}
		case "!trap", "!fatal":
			if login {
				return "authentication_failed", records, nil
			}
			trapped = "query_failed"
			if attrs["category"] == "9" || strings.Contains(strings.ToLower(attrs["message"]), "not enough permissions") {
				trapped = "permission_denied"
			}
			if words[0] == "!fatal" {
				return trapped, records, nil
			}
		default:
			return "invalid_reply", records, nil
		}
	}
	return "invalid_reply", records, nil
}

func writeSentence(w io.Writer, words []string) error {
	var b []byte
	for i := 0; i <= len(words); i++ {
		word := ""
		if i < len(words) {
			word = words[i]
		}
		n := len(word)
		if n > maxWord {
			return errReply
		}
		switch {
		case n < 0x80:
			b = append(b, byte(n))
		case n < 0x4000:
			b = append(b, byte(n>>8)|0x80, byte(n))
		case n < 0x200000:
			b = append(b, byte(n>>16)|0xc0, byte(n>>8), byte(n))
		case n < 0x10000000:
			b = append(b, byte(n>>24)|0xe0, byte(n>>16), byte(n>>8), byte(n))
		case uint64(n) <= uint64(^uint32(0)):
			b = append(b, 0xf0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		default:
			return errReply
		}
		b = append(b, word...)
	}
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func readSentence(r *bufio.Reader) ([]string, error) {
	var words []string
	total := 0
	for len(words) < 64 {
		first, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		var n uint32
		var extra int
		switch {
		case first < 0x80:
			n = uint32(first)
		case first < 0xc0:
			n, extra = uint32(first&0x3f), 1
		case first < 0xe0:
			n, extra = uint32(first&0x1f), 2
		case first < 0xf0:
			n, extra = uint32(first&0x0f), 3
		case first == 0xf0:
			extra = 4
		default:
			return nil, errReply
		}
		if extra == 4 {
			var buf [4]byte
			if _, err = io.ReadFull(r, buf[:]); err != nil {
				return nil, err
			}
			n = binary.BigEndian.Uint32(buf[:])
		} else {
			for range extra {
				v, e := r.ReadByte()
				if e != nil {
					return nil, e
				}
				n = n<<8 | uint32(v)
			}
		}
		if n == 0 {
			return words, nil
		}
		total += int(n)
		if n > maxWord || total > 128*1024 {
			return nil, errReply
		}
		buf := make([]byte, int(n))
		if _, err = io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		words = append(words, string(buf))
	}
	return nil, errReply
}
