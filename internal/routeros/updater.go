package routeros

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	PreviousContainerComment = "[Mikrozia:previous]"
	StagingCommentPrefix     = "[Mikrozia:staging:"
	CutoverSchedulerName     = "mikrozia-cutover"
	MinUpdateFreeDiskBytes   = 250 * 1024 * 1024 // 250 MiB minimum free storage on RouterOS
)

var validMikroziaTagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)-mikrozia$`)

// ValidateMikroziaTag validates that a Docker Hub tag matches the pinned Mikrozia release format.
func ValidateMikroziaTag(tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	if !validMikroziaTagRe.MatchString(tag) {
		return "", false
	}
	return tag, true
}

// ContainerUpdateSpec captures the live RouterOS /container configuration of an existing container.
type ContainerUpdateSpec struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Tag         string `json:"tag"`
	RemoteImage string `json:"remoteImage"`
	Interface   string `json:"interface"`
	RootDir     string `json:"rootDir"`
	Mountlists  string `json:"mountlists"`
	Envlist     string `json:"envlist"`
	DNS         string `json:"dns"`
	MemoryHigh  string `json:"memoryHigh"`
	MemoryMax   string `json:"memoryMax"`
	Logging     string `json:"logging"`
	StartOnBoot string `json:"startOnBoot"`
	Status      string `json:"status"`
	Comment     string `json:"comment"`
}

// ContainerUpdateInventory summarizes active, staged, and previous Mikrozia containers on RouterOS.
type ContainerUpdateInventory struct {
	State         string                `json:"state"`
	FreeHddSpace  uint64                `json:"freeHddSpace"`
	TotalHddSpace uint64                `json:"totalHddSpace"`
	Current       *ContainerUpdateSpec  `json:"current,omitempty"`
	Staged        *ContainerUpdateSpec  `json:"staged,omitempty"`
	Previous      []ContainerUpdateSpec `json:"previous,omitempty"`
	Detail        string                `json:"detail,omitempty"`
}

// StagingComment returns the marker comment used for a newly staged Mikrozia container.
func StagingComment(tag string) string {
	return StagingCommentPrefix + tag + "]"
}

// DeriveNextRootDir computes a sibling versioned root-dir from the current container's root-dir.
func DeriveNextRootDir(currentRootDir, targetTag string) string {
	dir := strings.TrimSpace(currentRootDir)
	if dir == "" {
		return "containers/mikrozia/root-" + targetTag
	}
	parent := path.Dir(strings.TrimRight(dir, "/"))
	if parent == "." || parent == "/" || parent == "" {
		return "containers/mikrozia/root-" + targetTag
	}
	return parent + "/root-" + targetTag
}

func rowToUpdateSpec(row map[string]string) ContainerUpdateSpec {
	status := strings.TrimSpace(row["status"])
	if status == "" {
		switch {
		case routerOSBool(row["extracting"]):
			status = "extracting"
		case routerOSBool(row["running"]):
			status = "running"
		case routerOSBool(row["stopped"]):
			status = "stopped"
		}
	}
	return ContainerUpdateSpec{
		ID:          strings.TrimSpace(row[".id"]),
		Name:        strings.TrimSpace(row["name"]),
		Tag:         strings.TrimSpace(row["tag"]),
		RemoteImage: strings.TrimSpace(row["remote-image"]),
		Interface:   strings.TrimSpace(row["interface"]),
		RootDir:     strings.TrimSpace(row["root-dir"]),
		Mountlists:  strings.TrimSpace(row["mountlists"]),
		Envlist:     strings.TrimSpace(row["envlist"]),
		DNS:         strings.TrimSpace(row["dns"]),
		MemoryHigh:  strings.TrimSpace(row["memory-high"]),
		MemoryMax:   strings.TrimSpace(row["memory-max"]),
		Logging:     strings.TrimSpace(row["logging"]),
		StartOnBoot: strings.TrimSpace(row["start-on-boot"]),
		Status:      status,
		Comment:     strings.TrimSpace(row["comment"]),
	}
}

// InspectContainersForUpdate reads disk headroom and all Mikrozia containers on RouterOS in read-only mode.
func (c Client) InspectContainersForUpdate(parent context.Context, cfg NATConnConfig, containerComment string) ContainerUpdateInventory {
	out := ContainerUpdateInventory{State: "unreachable"}
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		out.State = state
		return out
	}
	defer conn.Close()

	if writeSentence(conn, []string{"/system/resource/print", "=.proplist=free-hdd-space,total-hdd-space"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" && len(rows) > 0 {
			out.FreeHddSpace, _ = strconv.ParseUint(strings.TrimSpace(rows[0]["free-hdd-space"]), 10, 64)
			out.TotalHddSpace, _ = strconv.ParseUint(strings.TrimSpace(rows[0]["total-hdd-space"]), 10, 64)
		}
	}

	if writeSentence(conn, []string{
		"/container/print",
		"=.proplist=.id,name,tag,remote-image,interface,root-dir,mountlists,envlist,dns,memory-high,memory-max,logging,start-on-boot,status,running,stopped,extracting,comment",
	}) != nil {
		return out
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		out.State = state
		return out
	}

	commentTarget := strings.TrimSpace(containerComment)
	if commentTarget == "" {
		commentTarget = "[Mikrozia:container] Container"
	}

	for _, row := range rows {
		spec := rowToUpdateSpec(row)
		switch {
		case spec.Comment == commentTarget && out.Current == nil:
			cp := spec
			out.Current = &cp
		case strings.HasPrefix(spec.Comment, StagingCommentPrefix):
			cp := spec
			out.Staged = &cp
		case spec.Comment == PreviousContainerComment:
			out.Previous = append(out.Previous, spec)
		}
	}

	// Fallback: if no exact comment match, match a running container whose image is echodev7/mikrozia
	if out.Current == nil {
		for _, row := range rows {
			spec := rowToUpdateSpec(row)
			if strings.Contains(strings.ToLower(spec.Tag+spec.RemoteImage), "mikrozia") &&
				strings.EqualFold(spec.Status, "running") {
				cp := spec
				out.Current = &cp
				break
			}
		}
	}

	out.State = "ok"
	return out
}

// StageContainerUpdate creates a new versioned Mikrozia container on RouterOS reusing the current
// container's persistent mountlists, envlist, interface, and memory limits while writing to a new root-dir.
func (c Client) StageContainerUpdate(parent context.Context, cfg NATConnConfig, current ContainerUpdateSpec, targetTag string) (ContainerUpdateSpec, string, string) {
	cleanTag, ok := ValidateMikroziaTag(targetTag)
	if !ok {
		return ContainerUpdateSpec{}, "invalid_tag", "target version tag is not a valid Mikrozia release tag"
	}
	if strings.TrimSpace(current.Interface) == "" {
		return ContainerUpdateSpec{}, "invalid_container", "current container interface could not be determined"
	}

	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return ContainerUpdateSpec{}, state, ""
	}
	defer conn.Close()

	// Check free disk space before staging
	if writeSentence(conn, []string{"/system/resource/print", "=.proplist=free-hdd-space"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" && len(rows) > 0 {
			freeBytes, _ := strconv.ParseUint(strings.TrimSpace(rows[0]["free-hdd-space"]), 10, 64)
			if freeBytes > 0 && freeBytes < MinUpdateFreeDiskBytes {
				return ContainerUpdateSpec{}, "low_disk", fmt.Sprintf("RouterOS free disk space (%d MiB) is below the 250 MiB minimum required for staging", freeBytes/(1024*1024))
			}
		}
	}

	stagingComment := StagingComment(cleanTag)
	remoteImage := "echodev7/mikrozia:" + cleanTag
	nextRootDir := DeriveNextRootDir(current.RootDir, cleanTag)

	// Idempotency: if already staged for this tag, return it
	if writeSentence(conn, []string{
		"/container/print",
		"=.proplist=.id,name,tag,remote-image,interface,root-dir,mountlists,envlist,dns,memory-high,memory-max,logging,start-on-boot,status,running,stopped,extracting,comment",
	}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			for _, row := range rows {
				spec := rowToUpdateSpec(row)
				if spec.Comment == stagingComment || spec.RootDir == nextRootDir {
					return spec, "ok", ""
				}
			}
		}
	}

	words := []string{
		"/container/add",
		"=remote-image=" + remoteImage,
		"=interface=" + current.Interface,
		"=root-dir=" + nextRootDir,
		"=comment=" + stagingComment,
	}
	if current.Mountlists != "" {
		words = append(words, "=mountlists="+current.Mountlists)
	}
	if current.Envlist != "" {
		words = append(words, "=envlist="+current.Envlist)
	}
	if current.DNS != "" {
		words = append(words, "=dns="+current.DNS)
	}
	if current.MemoryHigh != "" {
		words = append(words, "=memory-high="+current.MemoryHigh)
	}
	if current.MemoryMax != "" {
		words = append(words, "=memory-max="+current.MemoryMax)
	}
	if current.Logging != "" {
		words = append(words, "=logging="+current.Logging)
	} else {
		words = append(words, "=logging=yes")
	}
	if current.StartOnBoot != "" {
		words = append(words, "=start-on-boot="+current.StartOnBoot)
	} else {
		words = append(words, "=start-on-boot=yes")
	}

	if writeSentence(conn, words) != nil {
		return ContainerUpdateSpec{}, "unreachable", ""
	}
	st, retID, detail := readCommandResult(r)
	if st != "ok" {
		return ContainerUpdateSpec{}, st, detail
	}

	return ContainerUpdateSpec{
		ID:          retID,
		RemoteImage: remoteImage,
		Interface:   current.Interface,
		RootDir:     nextRootDir,
		Mountlists:  current.Mountlists,
		Envlist:     current.Envlist,
		Status:      "extracting",
		Comment:     stagingComment,
	}, "ok", ""
}

// ScheduleContainerCutover creates a one-shot RouterOS scheduler that stops the current container,
// starts the newly staged container, and removes itself immediately. Requires RouterOS policy permission.
func (c Client) ScheduleContainerCutover(parent context.Context, cfg NATConnConfig, oldID, newID, activeComment string) (string, string) {
	oldID = strings.TrimSpace(oldID)
	newID = strings.TrimSpace(newID)
	if oldID == "" || newID == "" || oldID == newID {
		return "invalid_container", "both current and staged container IDs are required"
	}
	if strings.TrimSpace(activeComment) == "" {
		activeComment = "[Mikrozia:container] Container"
	}
	// Sanitize comment for RouterOS script string safety
	safeComment := strings.ReplaceAll(activeComment, `"`, "")
	safeComment = strings.ReplaceAll(safeComment, "$", "")
	safeComment = strings.ReplaceAll(safeComment, ";", "")

	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return state, ""
	}
	defer conn.Close()

	// Remove any stale cutover scheduler first (best-effort)
	if writeSentence(conn, []string{"/system/scheduler/print", "?name=" + CutoverSchedulerName, "=.proplist=.id"}) == nil {
		if st, rows := readNATRecords(r); st == "ok" {
			for _, row := range rows {
				if id := strings.TrimSpace(row[".id"]); id != "" {
					_ = writeSentence(conn, []string{"/system/scheduler/remove", "=.id=" + id})
					_, _, _ = readCommandResult(r)
				}
			}
		}
	}

	onEvent := fmt.Sprintf(
		`/system/scheduler/remove [find where name="%s"]; /container/set %s comment="%s" start-on-boot=no; /container/stop %s; :delay 3s; /container/set %s comment="%s" start-on-boot=yes; /container/start %s`,
		CutoverSchedulerName,
		oldID, PreviousContainerComment,
		oldID,
		newID, safeComment,
		newID,
	)

	if writeSentence(conn, []string{
		"/system/scheduler/add",
		"=name=" + CutoverSchedulerName,
		"=interval=3s",
		"=on-event=" + onEvent,
		"=comment=One-shot Mikrozia container cutover",
	}) != nil {
		return "unreachable", ""
	}
	st, _, detail := readCommandResult(r)
	if st != "ok" {
		if strings.Contains(strings.ToLower(detail), "permission") || st == "permission" {
			return "permission_required", detail
		}
		return st, detail
	}
	return "ok", ""
}

// RemovePreviousContainers removes only stopped containers marked with [Mikrozia:previous].
// Persistent mounts (/etc/x-ui and /root/cert) are never touched.
func (c Client) RemovePreviousContainers(parent context.Context, cfg NATConnConfig) (int, string, string) {
	conn, r, state := c.dialAndLoginNAT(parent, cfg)
	if state != "ok" {
		return 0, state, ""
	}
	defer conn.Close()

	if writeSentence(conn, []string{"/container/print", "=.proplist=.id,status,comment"}) != nil {
		return 0, "unreachable", ""
	}
	state, rows := readNATRecords(r)
	if state != "ok" {
		return 0, state, ""
	}
	removed := 0
	for _, row := range rows {
		if strings.TrimSpace(row["comment"]) != PreviousContainerComment {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(row["status"]), "stopped") {
			continue
		}
		if writeSentence(conn, []string{"/container/remove", "=.id=" + row[".id"]}) != nil {
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
