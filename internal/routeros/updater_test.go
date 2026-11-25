package routeros

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

type fakeUpdaterServer struct {
	containers  []map[string]string
	schedulers  []map[string]string
	freeHdd     string
	denySched   bool
	nextID      int
}

func (f *fakeUpdaterServer) dial(_ context.Context, _, _ string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	go f.serve(c2)
	return c1, nil
}

func (f *fakeUpdaterServer) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		words, err := readSentence(r)
		if err != nil || len(words) == 0 {
			return
		}
		cmd := words[0]
		attrs := map[string]string{}
		for _, w := range words[1:] {
			if strings.HasPrefix(w, "=") {
				k, v, _ := strings.Cut(w[1:], "=")
				attrs[k] = v
			}
		}
		switch cmd {
		case "/login":
			_ = writeSentence(conn, []string{"!done"})
		case "/system/resource/print":
			free := f.freeHdd
			if free == "" {
				free = "1073741824"
			}
			_ = writeSentence(conn, []string{"!re", "=free-hdd-space=" + free, "=total-hdd-space=2147483648"})
			_ = writeSentence(conn, []string{"!done"})
		case "/container/print":
			for _, row := range f.containers {
				out := []string{"!re"}
				for k, v := range row {
					out = append(out, "="+k+"="+v)
				}
				_ = writeSentence(conn, out)
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/container/add":
			f.nextID++
			id := "*C" + string(rune('0'+f.nextID))
			attrs[".id"] = id
			attrs["status"] = "extracting"
			f.containers = append(f.containers, attrs)
			_ = writeSentence(conn, []string{"!done", "=ret=" + id})
		case "/container/remove":
			var kept []map[string]string
			for _, row := range f.containers {
				if row[".id"] != attrs[".id"] {
					kept = append(kept, row)
				}
			}
			f.containers = kept
			_ = writeSentence(conn, []string{"!done"})
		case "/system/scheduler/print":
			for _, row := range f.schedulers {
				out := []string{"!re"}
				for k, v := range row {
					out = append(out, "="+k+"="+v)
				}
				_ = writeSentence(conn, out)
			}
			_ = writeSentence(conn, []string{"!done"})
		case "/system/scheduler/add":
			if f.denySched {
				_ = writeSentence(conn, []string{"!trap", "=message=not enough permissions"})
				_ = writeSentence(conn, []string{"!done"})
				continue
			}
			attrs[".id"] = "*S1"
			f.schedulers = append(f.schedulers, attrs)
			_ = writeSentence(conn, []string{"!done", "=ret=*S1"})
		default:
			_ = writeSentence(conn, []string{"!done"})
		}
	}
}

func TestContainerUpdaterLifecycle(t *testing.T) {
	srv := &fakeUpdaterServer{
		containers: []map[string]string{
			{
				".id":           "*1",
				"name":          "mikrozia-old",
				"interface":     "veth-mikrozia",
				"root-dir":      "containers/mikrozia/root-v1.0.9-mikrozia",
				"mountlists":    "MIKROZIA_DATA,MIKROZIA_CERT",
				"envlist":       "MIKROZIA_ENV",
				"memory-high":   "512M",
				"memory-max":    "1024M",
				"status":        "running",
				"comment":       "[Mikrozia:container] Container",
			},
			{
				".id":     "*0",
				"name":    "mikrozia-prev",
				"status":  "stopped",
				"comment": PreviousContainerComment,
			},
		},
	}
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	inv := client.InspectContainersForUpdate(context.Background(), cfg, "[Mikrozia:container] Container")
	if inv.State != "ok" || inv.Current == nil || inv.Current.ID != "*1" {
		t.Fatalf("InspectContainersForUpdate = %+v", inv)
	}
	if len(inv.Previous) != 1 {
		t.Fatalf("expected 1 previous container, got %d", len(inv.Previous))
	}

	staged, st, detail := client.StageContainerUpdate(context.Background(), cfg, *inv.Current, "v1.0.10-mikrozia")
	if st != "ok" || staged.RootDir != "containers/mikrozia/root-v1.0.10-mikrozia" {
		t.Fatalf("StageContainerUpdate = (%+v, %q, %q)", staged, st, detail)
	}

	st, detail = client.ScheduleContainerCutover(context.Background(), cfg, "*1", staged.ID, "[Mikrozia:container] Container")
	if st != "ok" || len(srv.schedulers) != 1 {
		t.Fatalf("ScheduleContainerCutover = (%q, %q), schedulers=%d", st, detail, len(srv.schedulers))
	}

	removed, st, detail := client.RemovePreviousContainers(context.Background(), cfg)
	if st != "ok" || removed != 1 {
		t.Fatalf("RemovePreviousContainers = (%d, %q, %q)", removed, st, detail)
	}
}

func TestStageContainerUpdate_LowDiskAndPermissionDeniedCutover(t *testing.T) {
	srv := &fakeUpdaterServer{freeHdd: "52428800", denySched: true} // 50 MiB
	client := Client{Dial: srv.dial}
	cfg := NATConnConfig{Host: "172.16.10.1", Port: 8728, Username: "mikrozia", Password: "secret"}

	_, st, _ := client.StageContainerUpdate(context.Background(), cfg, ContainerUpdateSpec{
		Interface: "veth-mikrozia",
		RootDir:   "containers/mikrozia/root-v1.0.9-mikrozia",
	}, "v1.0.10-mikrozia")
	if st != "low_disk" {
		t.Fatalf("expected low_disk, got %q", st)
	}

	st, _ = client.ScheduleContainerCutover(context.Background(), cfg, "*1", "*2", "[Mikrozia:container] Container")
	if st != "permission_required" {
		t.Fatalf("expected permission_required, got %q", st)
	}
}
