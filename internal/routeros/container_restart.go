package routeros

import (
	"bufio"
	"context"
	"net"
	"time"
)

type ContainerRestartResult struct {
	State string `json:"state"`
}

// RestartContainer rechecks identity with operational credentials immediately
// before sending one /container/restart command for the exact RouterOS ID.
func (c Client) RestartContainer(parent context.Context, host, username, password, comment, expectedID, expectedName string) ContainerRestartResult {
	if comment == "" || expectedID == "" || expectedName == "" {
		return ContainerRestartResult{State: "identity_unverified"}
	}
	if parent.Err() != nil {
		return ContainerRestartResult{State: "unreachable"}
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(host, "8728"))
	if err != nil {
		return ContainerRestartResult{State: "unreachable"}
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return ContainerRestartResult{State: "unreachable"}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	r := bufio.NewReader(conn)
	if writeSentence(conn, []string{"/login", "=name=" + username, "=password=" + password}) != nil {
		return ContainerRestartResult{State: "unreachable"}
	}
	state, _, _ := readReply(r, true)
	if state != "ok" {
		return ContainerRestartResult{State: state}
	}
	if writeSentence(conn, []string{"/container/print", "=.proplist=.id,name,comment,status", "?comment=" + comment}) != nil {
		return ContainerRestartResult{State: "unreachable"}
	}
	state, rows := readContainerRecords(r)
	if state != "ok" {
		return ContainerRestartResult{State: state}
	}
	if len(rows) != 1 || rows[0]["comment"] != comment || rows[0][".id"] != expectedID || rows[0]["name"] != expectedName {
		return ContainerRestartResult{State: "identity_unverified"}
	}
	if rows[0]["status"] != "running" {
		return ContainerRestartResult{State: "not_running"}
	}
	if writeSentence(conn, []string{"/container/restart", "=.id=" + expectedID}) != nil {
		return ContainerRestartResult{State: "uncertain"}
	}
	state, _, _ = readReply(r, false)
	if state == "ok" {
		return ContainerRestartResult{State: "accepted"}
	}
	if state == "unreachable" || state == "invalid_reply" {
		return ContainerRestartResult{State: "uncertain"}
	}
	return ContainerRestartResult{State: state}
}
