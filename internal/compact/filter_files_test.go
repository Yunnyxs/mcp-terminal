package compact

import (
	"strings"
	"testing"
)

func TestLsFilter(t *testing.T) {
	in := lines(
		"total 64",
		"drwxr-xr-x 16 alice alice 4096 Oct 21 19:36 .",
		"drwx------ 17 alice alice 4096 Oct 21 18:54 ..",
		"drwxr-xr-x  2 alice alice 4096 Oct 21 19:51 backup",
		"-rw-r--r--  1 alice alice 3980518 Oct 22 04:21 activity.log",
		"-rwxr-xr-x  1 alice alice 9000 Oct 22 04:21 run.sh",
		"-rw-------  1 alice alice  400 Oct 22 04:21 id_rsa",
		"lrwxrwxrwx  1 root root 11 Oct 22 04:21 link -> target",
		"-rw-r--r--  1 root alice 12 Oct 22 04:21 root.txt",
		"-rw-r--r--  1 alice alice 1048576 2026-10-22 04:21 iso.txt",
	)
	want := lines(
		"[owner alice:alice]",
		"backup/",
		"activity.log (3.8M)",
		"run.sh* (8.8K)",
		"id_rsa (400B) [600]",
		"link -> target (root:root)",
		"root.txt (12B) (root:alice)",
		"iso.txt (1M)",
	)
	wantOut(t, "ls -la", in, "ls", want, true)
}

func TestLsFilterPlainUntouched(t *testing.T) {
	plain := lines("a.go", "b.go", "dir")
	wantOut(t, "ls", plain, "ls", plain, false)
	odd := lines("total 8", "crw-rw-rw- 1 root root 1, 3 Oct 22 04:21 null", "-rw-r--r-- 1 u g 5 Oct 22 04:21 x")
	got, _, _ := run(t, "ls -l /dev", odd)
	if !strings.Contains(got, "crw-rw-rw- 1 root root 1, 3 Oct 22 04:21 null") || !strings.Contains(got, "x (5B)") {
		t.Errorf("device lines must stay intact and ordinary lines must be compacted: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0B", 999: "999B", 1024: "1K", 1536: "1.5K", 10240: "10K", 4096: "4K", 3980518: "3.8M", 1 << 30: "1G"}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
	if got := lsSize("4.0K"); got != "4.0K" {
		t.Errorf("size -h is left as is: %q", got)
	}
}

func TestLsMode(t *testing.T) {
	cases := map[string]string{"rwxr-xr-x": "755", "rw-r--r--": "644", "rw-------": "600", "rwsr-xr-x": "755", "rwxrwxrwt": "777", "---------": "000"}
	for in, want := range cases {
		if got := lsMode(in); got != want {
			t.Errorf("lsMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFindFilter(t *testing.T) {
	in := lines(
		"./a/b/x.go",
		"./a/b/y.go",
		"./a/z.go",
		"./c.go",
		"find: './secret': Permission denied",
		"./d/e.go",
		"./d/f.go",
		"./d/g.go",
	)
	want := lines(
		"./a/b/: x.go, y.go",
		"./a/z.go",
		"./c.go",
		"find: './secret': Permission denied",
		"./d/: e.go, f.go, g.go",
	)
	wantOut(t, "find . -name '*.go'", in, "find", want, false)
}

func TestFindFilterWrapsLongGroups(t *testing.T) {
	var ls []string
	for i := 0; i < 60; i++ {
		ls = append(ls, "./dir/file_number_"+strings.Repeat("x", 10)+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")
	}
	got, lossy := filterFind(ls)
	if lossy {
		t.Error("find lossless")
	}
	if len(got) < 2 {
		t.Fatalf("should have been split into several lines, got %d", len(got))
	}
	total := 0
	for _, g := range got {
		if len(g) > findMaxLine+40 {
			t.Errorf("line too long (%d): %q", len(g), g)
		}
		if !strings.HasPrefix(g, "./dir/:") {
			t.Errorf("every line must carry its directory: %q", g)
		}
		total += strings.Count(g, "file_number_")
	}
	if total != 60 {
		t.Errorf("all 60 names must be present, got %d", total)
	}
}

func TestTreeFilter(t *testing.T) {
	in := ".\n├── a\n│\u00a0\u00a0 ├── b.go\n│\u00a0\u00a0 └── c.go\n└── d\n    └── e.go\n\n2 directories, 3 files\n"
	want := lines(".", "  a", "    b.go", "    c.go", "  d", "    e.go", "", "2 directories, 3 files")
	wantOut(t, "tree", in, "tree", want, false)

	ascii := lines(".", "|-- a", "|   `-- b.go", "`-- c.go")
	wantOut(t, "tree --charset ascii", ascii, "tree", lines(".", "  a", "    b.go", "  c.go"), false)
}

func TestGrepFilterNumbered(t *testing.T) {
	in := lines(
		"./src/a.go:12:func foo() {",
		"./src/a.go:40:\tfoo()",
		"./src/b.go:7:// foo is here",
		"./README.md:3:foo",
		"./README.md:9:more foo",
	)
	want := lines(
		"./src/a.go",
		"  12: func foo() {",
		"  40: \tfoo()",
		"./src/b.go:7:// foo is here",
		"./README.md",
		"  3: foo",
		"  9: more foo",
	)
	wantOut(t, "grep -rn foo .", in, "grep/rg", want, false)
}

func TestGrepFilterPlain(t *testing.T) {
	in := lines("./src/a.go:func foo() {", "./src/a.go:\tfoo()", "./src/b.go:one")
	want := lines("./src/a.go", "  func foo() {", "  \tfoo()", "./src/b.go:one")
	wantOut(t, "rg foo .", in, "grep/rg", want, false)
}

func TestGrepFilterNoFalsePositives(t *testing.T) {
	single := lines("12:INFO:2026:started", "13:INFO:2026:ready", "key: value", "key: other")
	wantOut(t, "grep -n x app.log", single, "grep/rg", single, false)
	mixed := lines("grep: nope.txt: No such file or directory", "Binary file x.bin matches")
	wantOut(t, "grep -r foo .", mixed, "grep/rg", mixed, false)

	tricky := lines("a.go:12:30 started", "a.go:12:45 stopped")
	got, _, _ := run(t, "grep foo a.go b.go", tricky)
	if !strings.Contains(got, "12:30 started") && !strings.Contains(got, "30 started") {
		t.Errorf("content missing: %q", got)
	}
}

func TestGrepGroupable(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"grep", "-rn", "x", "."}, true},
		{[]string{"grep", "-rniE", "x", "."}, true},
		{[]string{"grep", "--include=*.go", "-r", "x"}, true},
		{[]string{"grep", "-c", "x", "f"}, false},
		{[]string{"grep", "-rl", "x"}, false},
		{[]string{"grep", "-C3", "x", "f"}, false},
		{[]string{"rg", "--json", "x"}, false},
		{[]string{"rg", "--context=2", "x"}, false},
		{[]string{"grep", "--", "-c", "f"}, true},
	} {
		if got := grepGroupable(c.args); got != c.want {
			t.Errorf("grepGroupable(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestBodyGrepSavesBytes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("./internal/some/long/package/path/file_a.go:")
		b.WriteString(strings.Repeat("1", 1+i%3))
		b.WriteString(":match number ")
		b.WriteString(strings.Repeat("x", i%5))
		b.WriteString("\n")
	}
	raw := b.String()
	got, _ := Body(raw, Options{Budget: 8000, Command: "grep -rn match ."})
	if len(got) >= len(raw)/2 {
		t.Errorf("result %d bytes of %d: should be less than half", len(got), len(raw))
	}
}
