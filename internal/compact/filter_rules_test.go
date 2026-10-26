package compact

import (
	"reflect"
	"strings"
	"testing"
)

func run(t *testing.T, cmd, in string) (out string, lossy bool, name string) {
	t.Helper()
	f := lookupFilter(cmd)
	if f == nil {
		t.Fatalf("lookupFilter(%q) = nil, want a filter", cmd)
	}
	ls, lossy := f.Apply(clean(in))
	return strings.Join(ls, "\n"), lossy, f.Name
}

func lines(ls ...string) string { return strings.Join(ls, "\n") }

func wantOut(t *testing.T, cmd, in, name, want string, wantLossy bool) {
	t.Helper()
	got, lossy, n := run(t, cmd, in)
	if n != name {
		t.Errorf("%q: filter = %q, want %q", cmd, n, name)
	}
	if got != want {
		t.Errorf("%q: wrong result.\n--- got ---\n%s\n--- want ---\n%s", cmd, got, want)
	}
	if lossy != wantLossy {
		t.Errorf("%q: lossy = %v, want %v", cmd, lossy, wantLossy)
	}
}

func TestStripSudo(t *testing.T) {
	cases := []struct{ in, want []string }{
		{[]string{"apt", "install"}, []string{"apt", "install"}},
		{[]string{"sudo", "apt", "install"}, []string{"apt", "install"}},
		{[]string{"sudo", "-n", "-E", "apt"}, []string{"apt"}},
		{[]string{"sudo", "-u", "bob", "ls"}, []string{"-u", "bob", "ls"}},
		{[]string{"sudo"}, []string{}},
	}
	for _, c := range cases {
		got := stripSudo(c.in)
		if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("stripSudo(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToolOf(t *testing.T) {
	cases := []struct {
		in       []string
		wantTool string
		wantRest []string
	}{
		{[]string{"pytest", "-q"}, "pytest", []string{"-q"}},
		{[]string{"/usr/bin/pytest"}, "pytest", []string{}},
		{[]string{"python3", "-m", "pytest", "-x"}, "pytest", []string{"-x"}},
		{[]string{"python3.12", "-m", "pip", "install", "x"}, "pip", []string{"install", "x"}},
		{[]string{"uv", "run", "pytest", "-q"}, "pytest", []string{"-q"}},
		{[]string{"poetry", "run", "python", "-m", "pytest"}, "pytest", []string{}},
		{[]string{"python", "script.py"}, "python", []string{"script.py"}},
		{nil, "", nil},
	}
	for _, c := range cases {
		tool, rest := toolOf(c.in)
		if tool != c.wantTool || len(rest) != len(c.wantRest) || (len(rest) > 0 && !reflect.DeepEqual(rest, c.wantRest)) {
			t.Errorf("toolOf(%q) = %q %q, want %q %q", c.in, tool, rest, c.wantTool, c.wantRest)
		}
	}
}

func TestFirstNonFlag(t *testing.T) {
	sub, rest := firstNonFlag([]string{"-q", "-o", "Dir=x", "install", "-y", "curl"}, "-o")
	if sub != "install" || !reflect.DeepEqual(rest, []string{"-y", "curl"}) {
		t.Errorf("firstNonFlag = %q %q", sub, rest)
	}
	if sub, _ := firstNonFlag([]string{"-q", "--yes"}); sub != "" {
		t.Errorf("options only: sub = %q, want empty", sub)
	}
}

func TestDropFilterKeepsImportant(t *testing.T) {
	rules := []dropRule{boiler(`^Collecting `), countDrop(`^Requirement `, "requirement", "requirements")}
	in := []string{"Collecting a", "Collecting error-handler", "Requirement ok 1", "Requirement failed 2", "done"}
	got, lossy := dropFilter(in, rules)
	want := []string{"Collecting error-handler", "Requirement failed 2", "done", "[1 requirement]"}
	if !reflect.DeepEqual(got, want) || !lossy {
		t.Errorf("dropFilter = %q lossy=%v, want %q lossy=true", got, lossy, want)
	}
}

func TestTidy(t *testing.T) {
	got := tidy([]string{"", "a", "", "", "b", ""})
	if want := []string{"a", "", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("tidy = %q, want %q", got, want)
	}
}

func TestLookupNewFilters(t *testing.T) {
	cases := map[string]string{
		"git status":                      "git status",
		"git -C /x --no-pager status -sb": "git status",
		"git diff --stat":                 "git diff",
		"git show HEAD":                   "git diff",
		"git log -n5":                     "git log",
		"git push origin main":            "git push/pull",
		"git commit -m x":                 "git commit",
		"git add -v .":                    "git add",
		"ls -la":                          "ls",
		"find . -name '*.go'":             "find",
		"tree -L 2":                       "tree",
		"grep -rn foo .":                  "grep/rg",
		"rg foo src":                      "grep/rg",
		"pytest -q":                       "pytest",
		"python3 -m pytest tests/":        "pytest",
		"uv run pytest":                   "pytest",
		"npm test":                        "js test",
		"npm run test:unit":               "js test",
		"yarn test --watchAll=false":      "js test",
		"pnpm exec vitest run":            "js test",
		"npx jest --ci":                   "js test",
		"cargo test --release":            "cargo test",
		"cargo +nightly test":             "cargo test",
		"cargo nextest run":               "cargo test",
		"pip install -r req.txt":          "pip",
		"python3 -m pip install x":        "pip",
		"uv pip install x":                "pip",
		"npm install":                     "npm/yarn/pnpm install",
		"npm ci":                          "npm/yarn/pnpm install",
		"yarn":                            "npm/yarn/pnpm install",
		"yarn add left-pad":               "npm/yarn/pnpm install",
		"pnpm i":                          "npm/yarn/pnpm install",
		"sudo apt-get install -y curl":    "apt",
		"apt -o Dpkg::Use-Pty=0 update":   "apt",
		"DEBIAN_FRONTEND=noninteractive apt-get upgrade -y": "apt",
		"docker ps -a":                                 "docker ps",
		"docker container ls":                          "docker ps",
		"docker compose ps":                            "docker ps",
		"systemctl status nginx":                       "systemctl status",
		"sudo systemctl --no-pager -n 20 status nginx": "systemctl status",
		"journalctl -u nginx -n 50 --no-pager":         "journalctl",
	}
	for cmd, want := range cases {
		f := lookupFilter(cmd)
		if f == nil || f.Name != want {
			got := "<nil>"
			if f != nil {
				got = f.Name
			}
			t.Errorf("lookupFilter(%q) = %s, want %s", cmd, got, want)
		}
	}

	for _, cmd := range []string{
		"git branch", "git stash list", "git checkout -b x",
		"find . -name x -exec rm {} ;", "find . -print0", "find . -delete",
		"tree -J", "grep -c foo f", "grep -l foo .", "rg --json foo", "grep -C3 foo f", "rg --heading foo",
		"pip list", "pip freeze", "pip show x", "npm run build", "npm start", "yarn --version", "apt list", "apt-get --help",
		"apt-get update && apt-get install -y x", "docker ps -q", "docker ps --format '{{.Names}}'", "docker images", "docker logs x",
		"systemctl restart nginx", "systemctl is-active x", "cargo build", "cargo run", "node app.js",
		"npm test | tail", "pytest > out.txt", "sudo -u bob ls",
	} {
		if f := lookupFilter(cmd); f != nil {
			t.Errorf("lookupFilter(%q) = %s, want nil (generic path)", cmd, f.Name)
		}
	}
}

func TestFiltersKeepImportantLines(t *testing.T) {
	cases := []struct{ cmd, in string }{
		{"git push", lines("Enumerating objects: 7, done.", "error: failed to push some refs to 'x'", "! [rejected] main -> main", "fatal: remote hung up")},
		{"git status", lines("On branch main", "  (use \"git add <file>...\" to include)", "\tboth modified:   a.go", "error: unmerged paths")},
		{"git add -v .", lines("add 'a.go'", "warning: LF will be replaced by CRLF in a.go.", "fatal: pathspec 'x' did not match")},
		{"pytest -v", lines("collected 2 items", "t.py::a PASSED [ 50%]", "t.py::b FAILED [100%]", "E   AssertionError", "1 failed, 1 passed in 0.1s")},
		{"cargo test", lines("   Compiling x v0.1.0", "warning: unused variable `y`", "error[E0308]: mismatched types", "test a ... ok", "test b ... FAILED")},
		{"pip install x", lines("Collecting x", "ERROR: Could not find a version that satisfies the requirement x", "WARNING: retrying")},
		{"npm install", lines("npm notice hi", "npm error code E404", "npm warn deprecated old@1.0.0", "npm ERR! failed")},
		{"apt-get install -y x", lines("Reading package lists... Done", "E: Unable to locate package x", "W: some warning", "Get:1 http://x y [1 kB]")},
		{"systemctl status x", lines("● x.service - X", "     Active: failed (Result: exit-code) since now", "    Process: 9 ExecStart=/bin/x (code=exited, status=1/FAILURE)", "     Memory: 1M", "Oct 1 00:00:00 h x[9]: fatal error: boom")},
		{"npm test", lines("PASS a.test.js", "  console.error", "    boom", "FAIL b.test.js", "  ● b › fails", "Tests: 1 failed, 1 passed")},
	}
	for _, c := range cases {
		out, _, _ := run(t, c.cmd, c.in)
		for _, l := range clean(c.in) {
			if isImportant(l) && !strings.Contains(out, l) {
				t.Errorf("%q: important line missing: %q\nresult:\n%s", c.cmd, l, out)
			}
		}
	}
}

func TestBodyUsesNewFilters(t *testing.T) {
	status := lines(
		"On branch main",
		"Changes not staged for commit:",
		`  (use "git add <file>..." to update what will be committed)`,
		`  (use "git restore <file>..." to discard changes in working directory)`,
		"\tmodified:   a.go",
		"",
		`no changes added to commit (use "git add" and/or "git commit -a")`,
	)
	got, lossy := Body(status, Options{Budget: 8000, Command: "git status"})
	if strings.Contains(got, "(use ") || !strings.Contains(got, "modified:   a.go") {
		t.Errorf("git status through Body = %q", got)
	}
	if lossy {
		t.Error("dropping git's built-in hints is not a loss of content: lossy must be false")
	}

	raw, _ := Body(status, Options{Budget: 8000})
	if !strings.Contains(raw, `(use "git add`) {
		t.Errorf("without Command it should not be filtered: %q", raw)
	}
}
