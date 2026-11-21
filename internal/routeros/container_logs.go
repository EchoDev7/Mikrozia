package routeros

import (
	"bufio"
	"context"
	"net"
	"time"
)

// ContainerLogEntry is a single record from RouterOS /container/log.
type ContainerLogEntry struct {
	Time    string `json:"time"`
	Message string `json:"message"`
}

type ContainerLogsSnapshot struct {
	State   string              `json:"state"`
	Entries []ContainerLogEntry `json:"entries"`
}

// ContainerLogs reads the RouterOS container log for the exact name supplied by
// a previously verified ContainerSnapshot. It never changes logging settings.
func (c Client) ContainerLogs(parent context.Context, host, username, password, name string, limit int) ContainerLogsSnapshot {
	if name == "" || limit < 1 || limit > 100 {
		return ContainerLogsSnapshot{State: "invalid_configuration", Entries: []ContainerLogEntry{}}
	}
	if parent.Err() != nil {
		return ContainerLogsSnapshot{State: "unreachable", Entries: []ContainerLogEntry{}}
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, "8728"))
	if err != nil {
		return ContainerLogsSnapshot{State: "unreachable", Entries: []ContainerLogEntry{}}
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return ContainerLogsSnapshot{State: "unreachable", Entries: []ContainerLogEntry{}}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	r := bufio.NewReader(conn)
	if writeSentence(conn, []string{"/login", "=name=" + username, "=password=" + password}) != nil {
		return ContainerLogsSnapshot{State: "unreachable", Entries: []ContainerLogEntry{}}
	}
	state, _, _ := readReply(r, true)
	if state != "ok" {
		return ContainerLogsSnapshot{State: state, Entries: []ContainerLogEntry{}}
	}
	if writeSentence(conn, []string{"/container/log/print", "=.proplist=time,container,message", "?container=" + name}) != nil {
		return ContainerLogsSnapshot{State: "unreachable", Entries: []ContainerLogEntry{}}
	}
	entries := make([]ContainerLogEntry, 0, limit)
	totalBytes := 0
	for range 2048 {
		words, err := readSentence(r)
		if err != nil || len(words) == 0 {
			return ContainerLogsSnapshot{State: "invalid_reply", Entries: []ContainerLogEntry{}}
		}
		for _, word := range words {
			totalBytes += len(word)
		}
		if totalBytes > 512*1024 {
			return ContainerLogsSnapshot{State: "invalid_reply", Entries: []ContainerLogEntry{}}
		}
		attrs, ok := parseContainerAttrs(words[1:])
		if !ok {
			return ContainerLogsSnapshot{State: "invalid_reply", Entries: []ContainerLogEntry{}}
		}
		switch words[0] {
		case "!re":
			if attrs["container"] != name || attrs["time"] == "" {
				return ContainerLogsSnapshot{State: "identity_unverified", Entries: []ContainerLogEntry{}}
			}
			entries = append(entries, ContainerLogEntry{Time: attrs["time"], Message: attrs["message"]})
			if len(entries) > limit {
				entries = entries[1:]
			}
		case "!done":
			return ContainerLogsSnapshot{State: "connected", Entries: entries}
		case "!empty":
		case "!trap", "!fatal":
			if attrs["category"] == "9" {
				return ContainerLogsSnapshot{State: "permission_denied", Entries: []ContainerLogEntry{}}
			}
			return ContainerLogsSnapshot{State: "query_failed", Entries: []ContainerLogEntry{}}
		default:
			return ContainerLogsSnapshot{State: "invalid_reply", Entries: []ContainerLogEntry{}}
		}
	}
	return ContainerLogsSnapshot{State: "invalid_reply", Entries: []ContainerLogEntry{}}
}
