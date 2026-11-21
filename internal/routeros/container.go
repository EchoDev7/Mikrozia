package routeros

import (
	"bufio"
	"context"
	"net"
	"strings"
	"time"
)

type ContainerSnapshot struct {
	State            string `json:"state"`
	ID               string `json:"-"`
	Name             string `json:"-"`
	Status           string `json:"status"`
	Interface        string `json:"interface"`
	Logging          bool   `json:"logging"`
	RestartAvailable bool   `json:"restartAvailable"`
}

// ContainerSnapshot reads one exact, operator-marked container.
func (c Client) ContainerSnapshot(parent context.Context, host, username, password, comment string) ContainerSnapshot {
	if parent.Err() != nil || comment == "" {
		return ContainerSnapshot{State: "not_configured"}
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, "8728"))
	if err != nil {
		return ContainerSnapshot{State: "unreachable"}
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return ContainerSnapshot{State: "unreachable"}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	r := bufio.NewReader(conn)
	if writeSentence(conn, []string{"/login", "=name=" + username, "=password=" + password}) != nil {
		return ContainerSnapshot{State: "unreachable"}
	}
	state, _, _ := readReply(r, true)
	if state != "ok" {
		return ContainerSnapshot{State: state}
	}
	if writeSentence(conn, []string{"/container/print", "=.proplist=.id,name,comment,status,interface,logging", "?comment=" + comment}) != nil {
		return ContainerSnapshot{State: "unreachable"}
	}
	state, rows := readContainerRecords(r)
	if state != "ok" {
		return ContainerSnapshot{State: state}
	}
	if len(rows) != 1 || rows[0]["comment"] != comment || rows[0][".id"] == "" || rows[0]["name"] == "" {
		return ContainerSnapshot{State: "identity_unverified"}
	}
	row := rows[0]
	return ContainerSnapshot{State: "connected", ID: row[".id"], Name: row["name"], Status: row["status"], Interface: row["interface"], Logging: row["logging"] == "yes"}
}

func readContainerRecords(r *bufio.Reader) (string, []map[string]string) {
	rows := make([]map[string]string, 0, 1)
	for range 64 {
		words, err := readSentence(r)
		if err != nil || len(words) == 0 {
			return "invalid_reply", nil
		}
		attrs, ok := parseContainerAttrs(words[1:])
		if !ok {
			return "invalid_reply", nil
		}
		switch words[0] {
		case "!re":
			if len(rows) >= 32 {
				return "invalid_reply", nil
			}
			rows = append(rows, attrs)
		case "!done":
			return "ok", rows
		case "!empty":
		case "!trap", "!fatal":
			if attrs["category"] == "9" {
				return "permission_denied", nil
			}
			return "query_failed", nil
		default:
			return "invalid_reply", nil
		}
	}
	return "invalid_reply", nil
}

func parseContainerAttrs(words []string) (map[string]string, bool) {
	attrs := make(map[string]string, len(words))
	for _, word := range words {
		if !strings.HasPrefix(word, "=") {
			return nil, false
		}
		key, value, ok := strings.Cut(word[1:], "=")
		if !ok || key == "" {
			return nil, false
		}
		attrs[key] = value
	}
	return attrs, true
}
