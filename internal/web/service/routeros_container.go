package service

import (
	"context"

	"github.com/echodev7/mikrozia/v3/internal/routeros"
)

func resolveRouterOSContainerComment() string {
	return resolveRouterOSAPIConfig().ContainerComment
}

type RouterOSContainerService struct {
	lookup   func(context.Context, string, string, string, string) routeros.ContainerSnapshot
	readLogs func(context.Context, string, string, string, string, int) routeros.ContainerLogsSnapshot
	restart  func(context.Context, string, string, string, string, string, string) routeros.ContainerRestartResult
}

func (s *RouterOSContainerService) Status(ctx context.Context) routeros.ContainerSnapshot {
	cfg := resolveRouterOSAPIConfig()
	if cfg.State != "connecting" {
		return routeros.ContainerSnapshot{State: cfg.State}
	}
	comment := cfg.ContainerComment
	if comment == "" {
		return routeros.ContainerSnapshot{State: "not_configured"}
	}
	lookup := s.lookup
	if lookup == nil {
		lookup = (routeros.Client{}).ContainerSnapshot
	}
	snap := lookup(ctx, cfg.Host, cfg.Username, cfg.Password, comment)
	snap.RestartAvailable = snap.State == "connected" && snap.Status == "running"
	return snap
}

func (s *RouterOSContainerService) Restart(ctx context.Context) routeros.ContainerRestartResult {
	cfg := resolveRouterOSAPIConfig()
	if cfg.State != "connecting" {
		return routeros.ContainerRestartResult{State: cfg.State}
	}
	snap := s.Status(ctx)
	if snap.State != "connected" {
		return routeros.ContainerRestartResult{State: snap.State}
	}
	if !snap.RestartAvailable {
		return routeros.ContainerRestartResult{State: "not_running"}
	}
	restart := s.restart
	if restart == nil {
		restart = (routeros.Client{}).RestartContainer
	}
	return restart(ctx, cfg.Host, cfg.Username, cfg.Password, cfg.ContainerComment, snap.ID, snap.Name)
}

func (s *RouterOSContainerService) Logs(ctx context.Context, limit int) routeros.ContainerLogsSnapshot {
	empty := []routeros.ContainerLogEntry{}
	if limit < 1 || limit > 100 {
		return routeros.ContainerLogsSnapshot{State: "invalid_configuration", Entries: empty}
	}
	snap := s.Status(ctx)
	if snap.State != "connected" {
		return routeros.ContainerLogsSnapshot{State: snap.State, Entries: empty}
	}
	if !snap.Logging {
		return routeros.ContainerLogsSnapshot{State: "logging_disabled", Entries: empty}
	}
	cfg := resolveRouterOSAPIConfig()
	if cfg.State != "connecting" {
		return routeros.ContainerLogsSnapshot{State: cfg.State, Entries: empty}
	}
	readLogs := s.readLogs
	if readLogs == nil {
		readLogs = (routeros.Client{}).ContainerLogs
	}
	return readLogs(ctx, cfg.Host, cfg.Username, cfg.Password, snap.Name, limit)
}
