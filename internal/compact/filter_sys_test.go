package compact

import (
	"fmt"
	"strings"
	"testing"
)

func psRow(cells ...string) string {
	widths := []int{15, 15, 25, 16, 30, 50}
	var b strings.Builder
	for i, c := range cells {
		if i < len(widths) {
			fmt.Fprintf(&b, "%-*s", widths[i], c)
		} else {
			b.WriteString(c)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestSysFiltersMatching(t *testing.T) {
	match := map[string]string{
		"docker ps":                    "docker ps",
		"docker ps -a":                 "docker ps",
		"docker container ls":          "docker ps",
		"docker compose ps":            "docker ps",
		"/usr/bin/docker ps":           "docker ps",
		"systemctl status nginx":       "systemctl status",
		"systemctl -n 50 status nginx": "systemctl status",
		"systemctl --user status x":    "systemctl status",
		"sudo systemctl status nginx":  "systemctl status",
		"journalctl -u nginx -n 50":    "journalctl",
		"journalctl --no-pager -b":     "journalctl",
		"sudo journalctl -u nginx":     "journalctl",
	}
	for cmd, name := range match {
		if f := lookupFilter(cmd); f == nil || f.Name != name {
			t.Errorf("lookupFilter(%q) = %v, want %s", cmd, f, name)
		}
	}
	for _, cmd := range []string{
		"docker ps -q", "docker ps -aq", "docker ps --quiet", "docker ps --format=table", "docker images", "docker logs web",
		"systemctl restart nginx", "systemctl is-active nginx", "systemctl list-units", "systemctl",
		"sudo", "sudo -n",
	} {
		if f := lookupFilter(cmd); f != nil && (f.Name == "docker ps" || f.Name == "systemctl status" || f.Name == "journalctl") {
			t.Errorf("lookupFilter(%q) = %s, want nil", cmd, f.Name)
		}
	}
}

func TestDockerPsFilterTable(t *testing.T) {
	in := lines(
		psRow("CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES"),
		psRow("a1b2c3d4e5f6", "nginx:latest", `"/docker-entrypoint.sh"`, "2 hours ago", "Up 2 hours", "0.0.0.0:80->80/tcp, :::80->80/tcp", "web"),
		psRow("f6e5d4c3b2a1", "redis:7", `"docker-entrypoint.s"`, "3 days ago", "Exited (1) 3 days ago", "", "cache"),
		psRow("0a0b0c0d0e0f", "app:dev", `"/app"`, "5 min ago", "Restarting (1) 5 seconds ago", "[::]:8080->8080/tcp, 0.0.0.0:8080->8080/tcp", "api"),
	)
	want := lines(
		"NAMES  IMAGE  STATUS  PORTS",
		"web  nginx:latest  Up 2 hours  0.0.0.0:80->80/tcp",
		"cache  redis:7  Exited (1) 3 days ago  -",
		"api  app:dev  Restarting (1) 5 seconds ago  0.0.0.0:8080->8080/tcp",
	)
	wantOut(t, "docker ps -a", in, "docker ps", want, true)
}

func TestDockerPsFilterCompose(t *testing.T) {
	hdr := fmt.Sprintf("%-12s%-14s%-18s%-14s%-16s%-24s%s", "NAME", "IMAGE", "COMMAND", "SERVICE", "CREATED", "STATUS", "PORTS")
	row := fmt.Sprintf("%-12s%-14s%-18s%-14s%-16s%-24s%s", "proj-db-1", "postgres:16", `"postgres"`, "db", "1 hour ago", "Up 1 hour (healthy)", "5432/tcp")
	want := lines(
		"NAME  SERVICE  IMAGE  STATUS  PORTS",
		"proj-db-1  db  postgres:16  Up 1 hour (healthy)  5432/tcp",
	)
	wantOut(t, "docker compose ps", lines(hdr, row), "docker ps", want, true)
}

func TestDockerPsFilterLeavesNonTables(t *testing.T) {
	for _, in := range []string{
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?",
		lines("permission denied while trying to connect to the Docker daemon socket", "Got permission denied"),
		psRow("CONTAINER ID", "IMAGE", "COMMAND"),
		lines(psRow("CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES")),
	} {
		wantOut(t, "docker ps", in, "docker ps", in, false)
	}
}

func TestDedupePorts(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"80/tcp", "80/tcp"},
		{"0.0.0.0:80->80/tcp, :::80->80/tcp", "0.0.0.0:80->80/tcp"},
		{"0.0.0.0:80->80/tcp, [::]:80->80/tcp, 0.0.0.0:443->443/tcp", "0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp"},
		{":::80->80/tcp", ":::80->80/tcp"},
	}
	for _, c := range cases {
		if got := dedupePorts(c.in); got != c.want {
			t.Errorf("dedupePorts(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSystemctlStatusActive(t *testing.T) {
	in := lines(
		"● nginx.service - A high performance web server",
		"     Loaded: loaded (/lib/systemd/system/nginx.service; enabled; preset: enabled)",
		"     Active: active (running) since Wed 2026-10-21 10:00:00 UTC; 1h ago",
		"       Docs: man:nginx(8)",
		"    Process: 1234 ExecStartPre=/usr/sbin/nginx -t (code=exited, status=0/SUCCESS)",
		"   Main PID: 1236 (nginx)",
		"      Tasks: 3 (limit: 4600)",
		"     Memory: 5.0M",
		"        CPU: 20ms",
		"     CGroup: /system.slice/nginx.service",
		`             ├─1236 "nginx: master process"`,
		`             └─1237 "nginx: worker process"`,
		"",
		"Oct 21 10:00:00 host systemd[1]: Starting nginx...",
		"Oct 21 10:00:00 host systemd[1]: Started nginx.",
	)
	want := lines(
		"● nginx.service - A high performance web server",
		"     Loaded: loaded (/lib/systemd/system/nginx.service; enabled; preset: enabled)",
		"     Active: active (running) since Wed 2026-10-21 10:00:00 UTC; 1h ago",
		"   Main PID: 1236 (nginx)",
		"",
		"Oct 21 10:00:00 host systemd[1]: Starting nginx...",
		"Oct 21 10:00:00 host systemd[1]: Started nginx.",
	)
	wantOut(t, "systemctl status nginx", in, "systemctl status", want, true)
}

func TestSystemctlStatusFailedKeepsFailure(t *testing.T) {
	in := lines(
		"× app.service - My App",
		"     Loaded: loaded (/etc/systemd/system/app.service; enabled; preset: enabled)",
		"     Active: failed (Result: exit-code) since Wed 2026-10-21 10:05:00 UTC; 5s ago",
		"    Process: 4321 ExecStart=/usr/bin/app (code=exited, status=1/FAILURE)",
		"   Main PID: 4321 (code=exited, status=1/FAILURE)",
		"        CPU: 12ms",
		"",
		"Oct 21 10:05:00 host app[4321]: error: cannot bind port 8080",
		"Oct 21 10:05:00 host systemd[1]: app.service: Failed with result 'exit-code'.",
	)
	got, _, _ := run(t, "systemctl status app", in)
	for _, must := range []string{
		"× app.service - My App",
		"Active: failed (Result: exit-code)",
		"Main PID: 4321 (code=exited, status=1/FAILURE)",
		"error: cannot bind port 8080",
		"Failed with result 'exit-code'.",
		"Process: 4321 ExecStart=/usr/bin/app (code=exited, status=1/FAILURE)",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("missing from the result: %q\n--- got ---\n%s", must, got)
		}
	}
}

func TestSystemctlStatusKeepsNonZeroProcessLines(t *testing.T) {
	in := lines(
		"× app.service - My App",
		"     Active: failed (Result: exit-code)",
		"    Process: 99 ExecStartPre=/usr/bin/check (code=exited, status=203/EXEC)",
		"    Process: 100 ExecStart=/usr/bin/app (code=killed, signal=SEGV)",
		"    Process: 101 ExecStopPost=/usr/bin/clean (code=exited, status=0/SUCCESS)",
	)
	want := lines(
		"× app.service - My App",
		"     Active: failed (Result: exit-code)",
		"    Process: 99 ExecStartPre=/usr/bin/check (code=exited, status=203/EXEC)",
		"    Process: 100 ExecStart=/usr/bin/app (code=killed, signal=SEGV)",
	)
	wantOut(t, "systemctl status app", in, "systemctl status", want, true)
}

func TestSystemctlStatusDropsPagerFooter(t *testing.T) {
	in := lines(
		"● x.service - X",
		"     Active: active (running)",
		"Hint: Some lines were ellipsized, use -l to show in full.",
		"lines 1-12/12 (END)",
	)
	wantOut(t, "systemctl status x", in, "systemctl status", lines("● x.service - X", "     Active: active (running)"), false)
}

func TestJournalctlFactorsOutHost(t *testing.T) {
	in := lines(
		"-- Logs begin at Mon 2026-10-19 08:00:00 UTC, end at Wed 2026-10-21 10:00:00 UTC. --",
		"Oct 21 10:00:00 myhost systemd[1]: Started thing.",
		"Oct  5 10:00:01 myhost app[10]: error: boom",
		"    continuation line without timestamp",
	)
	want := lines(
		"[host: myhost]",
		"Oct 21 10:00:00 systemd[1]: Started thing.",
		"Oct  5 10:00:01 app[10]: error: boom",
		"    continuation line without timestamp",
	)
	wantOut(t, "journalctl -u thing", in, "journalctl", want, false)
}

func TestJournalctlISOFormat(t *testing.T) {
	in := lines(
		"2026-10-21T10:00:00+0000 myhost app[10]: started",
		"2026-10-21T10:00:01+0000 myhost app[10]: panic: nil pointer",
	)
	want := lines(
		"[host: myhost]",
		"2026-10-21T10:00:00+0000 app[10]: started",
		"2026-10-21T10:00:01+0000 app[10]: panic: nil pointer",
	)
	wantOut(t, "journalctl -o short-iso", in, "journalctl", want, false)
}

func TestJournalctlLeavesMultiHostAndEmpty(t *testing.T) {
	multi := lines(
		"Oct 21 10:00:00 host-a app[1]: hello",
		"Oct 21 10:00:01 host-b app[2]: world",
	)
	wantOut(t, "journalctl -D /var/log/journal", multi, "journalctl", multi, false)
	wantOut(t, "journalctl -u none", "-- No entries --", "journalctl", "-- No entries --", false)
}

func TestJournalctlKeepsEveryImportantMessage(t *testing.T) {
	msgs := []string{
		"kernel: Out of memory: Killed process 4242 (java)",
		"sshd[7]: Failed password for root from 10.0.0.9 port 2222 ssh2",
		"app[3]: fatal error: all goroutines are asleep - deadlock!",
		"systemd[1]: nginx.service: Main process exited, code=exited, status=1/FAILURE",
	}
	var in []string
	for _, m := range msgs {
		in = append(in, "Oct 21 10:00:00 myhost "+m)
	}
	got, _, _ := run(t, "journalctl -p err", lines(in...))
	for _, m := range msgs {
		if !strings.Contains(got, m) {
			t.Errorf("message missing or changed: %q\n--- got ---\n%s", m, got)
		}
	}
	if strings.Contains(got, "myhost ") {
		t.Errorf("the host name is still on every line:\n%s", got)
	}
}

func TestBodyJournalctlKeepsErrorLine(t *testing.T) {
	var ls []string
	for i := 0; i < 40; i++ {
		ls = append(ls, fmt.Sprintf("Oct 21 10:00:%02d prod-server-01 app[10]: tick %d", i, i))
	}
	ls = append(ls, "Oct 21 10:01:00 prod-server-01 app[10]: error: database unreachable")
	got, _ := Body(lines(ls...), Options{Budget: 10000, Command: "journalctl -u app -n 41"})
	if !strings.HasPrefix(got, "[host: prod-server-01]") {
		t.Errorf("the host was not moved to the start:\n%s", got)
	}
	if !strings.Contains(got, "error: database unreachable") {
		t.Errorf("the error line is missing:\n%s", got)
	}
	if strings.Contains(got, "prod-server-01 app") {
		t.Errorf("the host is still repeated on every line:\n%s", got)
	}
}
