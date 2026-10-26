package compact

import "testing"

func TestGitStatusFilter(t *testing.T) {
	in := lines(
		"On branch main",
		"Your branch is ahead of 'origin/main' by 1 commit.",
		`  (use "git push" to publish your local commits)`,
		"",
		"Changes not staged for commit:",
		`  (use "git add <file>..." to update what will be committed)`,
		`  (use "git restore <file>..." to discard changes in working directory)`,
		"\tmodified:   internal/compact/filter.go",
		"",
		"Untracked files:",
		`  (use "git add <file>..." to include in what will be committed)`,
		"\tinternal/compact/filter_git.go",
		"",
		`no changes added to commit (use "git add" and/or "git commit -a")`,
	)
	want := lines(
		"On branch main",
		"Your branch is ahead of 'origin/main' by 1 commit.",
		"",
		"Changes not staged for commit:",
		"\tmodified:   internal/compact/filter.go",
		"",
		"Untracked files:",
		"\tinternal/compact/filter_git.go",
	)
	wantOut(t, "git status", in, "git status", want, false)
}

func TestGitDiffFilter(t *testing.T) {
	in := lines(
		"diff --git a/x.go b/x.go",
		"index 83db48f..bf2a3c1 100644",
		"--- a/x.go",
		"+++ b/x.go",
		"@@ -1,3 +1,4 @@",
		" package x",
		"+var a = 1",
		"diff --git a/y.go b/y.go",
		"new file mode 100644",
		"index 0000000..e69de29",
	)
	want := lines(
		"diff --git a/x.go b/x.go",
		"--- a/x.go",
		"+++ b/x.go",
		"@@ -1,3 +1,4 @@",
		" package x",
		"+var a = 1",
		"diff --git a/y.go b/y.go",
		"new file mode 100644",
	)
	wantOut(t, "git diff", in, "git diff", want, false)
}

func TestGitLogFilter(t *testing.T) {
	in := lines(
		"commit 3f2a9c1d8e7b6a5f4e3d2c1b0a99887766554433 (HEAD -> main, origin/main)",
		"Author: alice <alice@example.com>",
		"Date:   Wed Oct 21 19:36:12 2026 +0700",
		"",
		"    Add go test filter",
		"",
		"    Long message body",
		"    second line.",
		"",
		"commit 1111111111111111111111111111111111111111",
		"Merge: aaaaaaa bbbbbbb",
		"Author: Bob Smith <bob@example.com>",
		"Date:   Tue Oct 20 10:00:00 2026 +0700",
		"",
		"    Merge branch 'x'",
		"",
		"commit 2222222222222222222222222222222222222222",
		"Author: Sue <sue@example.com>",
		"Date:   Mon Oct 19 09:00:00 2026 +0700",
	)
	want := lines(
		"3f2a9c1 (HEAD -> main, origin/main) Add go test filter (alice, 2026-10-21)",
		"1111111 Merge branch 'x' (Bob Smith, 2026-10-20)",
		"2222222 (no message) (Sue, 2026-10-19)",
	)
	wantOut(t, "git log", in, "git log", want, true)
}

func TestGitLogOtherFormatsUntouched(t *testing.T) {
	oneline := lines("abc1234 first", "def5678 second")
	wantOut(t, "git log --oneline", oneline, "git log", oneline, false)
	withPatch := lines(
		"commit 3f2a9c1d8e7b6a5f4e3d2c1b0a99887766554433",
		"Author: A <a@x>",
		"Date:   Wed Oct 21 19:36:12 2026 +0700",
		"",
		"    msg",
		"",
		"diff --git a/x b/x",
		"+x",
	)
	wantOut(t, "git log -p", withPatch, "git log", withPatch, false)
	wantOut(t, "git log", "fatal: not a git repository", "git log", "fatal: not a git repository", false)
}

func TestGitTransferFilter(t *testing.T) {
	in := lines(
		"Enumerating objects: 7, done.",
		"Counting objects: 100% (7/7), done.",
		"Delta compression using up to 8 threads",
		"Compressing objects: 100% (4/4), done.",
		"Writing objects: 100% (4/4), 1.20 KiB | 1.20 MiB/s, done.",
		"Total 4 (delta 3), reused 0 (delta 0), pack-reused 0",
		"remote: Resolving deltas: 100% (3/3), completed with 3 local objects.",
		"remote:",
		"remote: Create a pull request for 'x' on GitHub by visiting:",
		"remote:      https://github.com/u/r/pull/new/x",
		"To github.com:u/r.git",
		"   abc1234..def5678  main -> main",
	)
	want := lines(
		"remote: Create a pull request for 'x' on GitHub by visiting:",
		"remote:      https://github.com/u/r/pull/new/x",
		"To github.com:u/r.git",
		"   abc1234..def5678  main -> main",
	)
	wantOut(t, "git push origin x", in, "git push/pull", want, false)

	rejected := lines(
		"To github.com:u/r.git",
		" ! [rejected]        main -> main (fetch first)",
		"error: failed to push some refs to 'github.com:u/r.git'",
		"hint: Updates were rejected because the remote contains work that you do not",
		"hint: have locally.",
	)
	wantOut(t, "git push", rejected, "git push/pull", rejected, false)
}

func TestGitCommitAndAddFilter(t *testing.T) {
	commit := lines(
		"[main abc1234] x",
		" 3 files changed, 30 insertions(+)",
		" create mode 100644 a.go",
		" create mode 100644 b.go",
		" create mode 100644 c.go",
		" delete mode 100644 old.go",
	)
	want := lines(
		"[main abc1234] x",
		" 3 files changed, 30 insertions(+)",
		"[3 files created]",
		"[1 file deleted]",
	)
	wantOut(t, "git commit -m x", commit, "git commit", want, true)

	add := lines("add 'a.go'", "add 'b.go'", "remove 'c.go'")
	wantOut(t, "git add -v .", add, "git add", lines("[2 files added]", "[1 file removed]"), true)
}
