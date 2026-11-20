package sys

type ContainerMemory struct {
	Used  uint64  `json:"used"`
	Limit *uint64 `json:"limit"`
}

type LXCStats struct {
	Scope               string
	CPU                 float64
	CPUs                int
	SwapUsed, SwapTotal uint64
	Uptime              uint64
	ContainerMem        *ContainerMemory
	Unavailable         []string
}
