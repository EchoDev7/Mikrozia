package sys

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func readContainerMemory(proc, cg string, hasV2Root, hasV1Memory, initAtV2Root bool) *ContainerMemory {
	if hasV2Root && initAtV2Root {
		return readCgroupV2Memory(cg)
	}
	if hasV1Memory && containerV1MemoryAtRoot(proc) {
		return readCgroupV1Memory(cg)
	}
	return nil
}

func readCgroupV2Memory(cg string) *ContainerMemory {
	const unlimited = uint64(1) << 62
	usedRaw, err := os.ReadFile(filepath.Join(cg, "memory.current"))
	if err != nil {
		return nil
	}
	used, err := strconv.ParseUint(strings.TrimSpace(string(usedRaw)), 10, 64)
	if err != nil {
		return nil
	}
	maxRaw, err := os.ReadFile(filepath.Join(cg, "memory.max"))
	if err != nil {
		return nil
	}
	s := strings.TrimSpace(string(maxRaw))
	if s == "" {
		return nil
	}
	if s == "max" {
		return &ContainerMemory{Used: used, Limit: nil}
	}
	limitVal, err := strconv.ParseUint(s, 10, 64)
	if err != nil || limitVal == 0 {
		return nil
	}
	if limitVal >= unlimited {
		return &ContainerMemory{Used: used, Limit: nil}
	}
	return &ContainerMemory{Used: used, Limit: &limitVal}
}

func readCgroupV1Memory(cg string) *ContainerMemory {
	const unlimited = uint64(1) << 62
	memDir := filepath.Join(cg, "memory")
	usedRaw, err := os.ReadFile(filepath.Join(memDir, "memory.usage_in_bytes"))
	if err != nil {
		return nil
	}
	used, err := strconv.ParseUint(strings.TrimSpace(string(usedRaw)), 10, 64)
	if err != nil {
		return nil
	}
	limRaw, err := os.ReadFile(filepath.Join(memDir, "memory.limit_in_bytes"))
	if err != nil {
		return nil
	}
	limitVal, err := strconv.ParseUint(strings.TrimSpace(string(limRaw)), 10, 64)
	if err != nil || limitVal == 0 {
		return nil
	}
	if limitVal >= unlimited {
		return &ContainerMemory{Used: used, Limit: nil}
	}
	return &ContainerMemory{Used: used, Limit: &limitVal}
}

func containerV1MemoryAtRoot(proc string) bool {
	data, err := os.ReadFile(filepath.Join(proc, "1/cgroup"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		for _, subsys := range strings.Split(parts[1], ",") {
			if subsys == "memory" && (parts[2] == "/" || parts[2] == "/init.scope") {
				return true
			}
		}
	}
	return false
}
