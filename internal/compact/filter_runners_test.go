package compact

import (
	"strings"
	"testing"
)

func TestPytestFilterVerbose(t *testing.T) {
	in := lines(
		"============================= test session starts ==============================",
		"platform linux -- Python 3.12.3, pytest-8.1.1, pluggy-1.4.0",
		"rootdir: /home/u/proj",
		"plugins: cov-4.1.0",
		"collected 4 items",
		"",
		"tests/test_a.py::test_one PASSED                                         [ 25%]",
		"tests/test_a.py::test_two PASSED                                         [ 50%]",
		"tests/test_b.py::test_three FAILED                                       [ 75%]",
		"tests/test_b.py::test_four PASSED                                        [100%]",
		"",
		"=================================== FAILURES ===================================",
		"________________________________ test_three ____________________________________",
		"",
		"    def test_three():",
		">       assert 1 == 2",
		"E       assert 1 == 2",
		"",
		"tests/test_b.py:5: AssertionError",
		"=========================== short test summary info ============================",
		"FAILED tests/test_b.py::test_three - assert 1 == 2",
		"========================= 1 failed, 3 passed in 0.12s ==========================",
	)
	want := lines(
		"tests/test_b.py::test_three FAILED                                       [ 75%]",
		"",
		"=================================== FAILURES ===================================",
		"________________________________ test_three ____________________________________",
		"",
		"    def test_three():",
		">       assert 1 == 2",
		"E       assert 1 == 2",
		"",
		"tests/test_b.py:5: AssertionError",
		"=========================== short test summary info ============================",
		"FAILED tests/test_b.py::test_three - assert 1 == 2",
		"========================= 1 failed, 3 passed in 0.12s ==========================",
	)
	wantOut(t, "pytest -v", in, "pytest", want, true)
}

func TestPytestFilterDots(t *testing.T) {
	in := lines(
		"tests/test_a.py ..                                                          [ 50%]",
		"tests/test_b.py .F                                                          [100%]",
		"=========================== 1 failed, 3 passed in 0.12s ===========================",
	)
	want := lines(
		"tests/test_b.py .F                                                          [100%]",
		"=========================== 1 failed, 3 passed in 0.12s ===========================",
	)
	wantOut(t, "python3 -m pytest", in, "pytest", want, false)

	wantOut(t, "pytest -q", lines("....                [100%]", "4 passed in 0.01s"), "pytest", "4 passed in 0.01s", false)
}

func TestJSTestFilterJest(t *testing.T) {
	in := lines(
		"> pkg@1.0.0 test",
		"> jest",
		"",
		"PASS src/a.test.js",
		"  ✓ adds (2 ms)",
		"  console.warn",
		"    something odd",
		"",
		"      at Object.<anonymous> (src/a.test.js:3:11)",
		"",
		"PASS src/c.test.js",
		"  ✓ more (1 ms)",
		"",
		"FAIL src/b.test.js",
		"  math",
		"    ✓ ok (1 ms)",
		"    ✕ fails (3 ms)",
		"",
		"  ● math › fails",
		"",
		"    expect(received).toBe(expected)",
		"",
		"Test Suites: 1 failed, 2 passed, 3 total",
		"Tests:       1 failed, 3 passed, 4 total",
	)
	want := lines(
		"  console.warn",
		"    something odd",
		"",
		"      at Object.<anonymous> (src/a.test.js:3:11)",
		"",
		"FAIL src/b.test.js",
		"  math",
		"    ✕ fails (3 ms)",
		"",
		"  ● math › fails",
		"",
		"    expect(received).toBe(expected)",
		"",
		"Test Suites: 1 failed, 2 passed, 3 total",
		"Tests:       1 failed, 3 passed, 4 total",
	)
	wantOut(t, "npx jest", in, "js test", want, true)
}

func TestJSTestFilterMochaDropsEmptyGroups(t *testing.T) {
	in := lines(
		"> pkg@1.0.0 test",
		"> mocha",
		"",
		"  Array",
		"    #indexOf()",
		"      ✔ should return -1",
		"      ✔ should return index",
		"  Other",
		"    ✔ one",
		"",
		"  3 passing (5ms)",
	)
	wantOut(t, "npm test", in, "js test", "  3 passing (5ms)", true)

	failing := lines(
		"  Array",
		"    #indexOf()",
		"      ✔ ok",
		"      1) bad",
		"",
		"  1 passing (5ms)",
		"  1 failing",
	)
	want := lines(
		"  Array",
		"    #indexOf()",
		"      1) bad",
		"",
		"  1 passing (5ms)",
		"  1 failing",
	)
	wantOut(t, "npx mocha", failing, "js test", want, true)
}

func TestJSTestFilterVitestAndBun(t *testing.T) {
	vitest := lines(
		" ✓ src/a.test.ts (3 tests) 5ms",
		" ❯ src/b.test.ts (2 tests | 1 failed) 8ms",
		"   ✓ ok",
		"   × breaks",
		"",
		" Test Files  1 failed | 1 passed (2)",
		"      Tests  1 failed | 4 passed (5)",
	)
	want := lines(
		" ❯ src/b.test.ts (2 tests | 1 failed) 8ms",
		"   × breaks",
		"",
		" Test Files  1 failed | 1 passed (2)",
		"      Tests  1 failed | 4 passed (5)",
	)
	wantOut(t, "pnpm exec vitest run", vitest, "js test", want, true)
	bun := lines("(pass) adds [0.1ms]", "(fail) breaks [0.2ms]", " 1 pass", " 1 fail")
	wantOut(t, "bun test", bun, "js test", lines("(fail) breaks [0.2ms]", " 1 pass", " 1 fail"), true)
}

func TestCargoTestFilter(t *testing.T) {
	in := lines(
		"   Compiling foo v0.1.0 (/p)",
		"    Finished test [unoptimized + debuginfo] target(s) in 1.2s",
		"     Running unittests src/lib.rs (target/debug/deps/foo-abc)",
		"",
		"running 3 tests",
		"test a ... ok",
		"test b ... ok",
		"test c ... FAILED",
		"",
		"failures:",
		"",
		"---- c stdout ----",
		"thread 'c' panicked at src/lib.rs:9:5",
		"",
		"test result: FAILED. 2 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out",
	)
	want := lines(
		"     Running unittests src/lib.rs (target/debug/deps/foo-abc)",
		"",
		"test c ... FAILED",
		"",
		"failures:",
		"",
		"---- c stdout ----",
		"thread 'c' panicked at src/lib.rs:9:5",
		"",
		"test result: FAILED. 2 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out",
	)
	wantOut(t, "cargo test", in, "cargo test", want, true)

	nextest := lines(
		"   Compiling foo v0.1.0",
		"    Starting 2 tests across 1 binary",
		"        PASS [   0.004s] foo tests::a",
		"        FAIL [   0.005s] foo tests::b",
		"     Summary [   0.010s] 2 tests run: 1 passed, 1 failed",
	)
	wantOut(t, "cargo nextest run", nextest, "cargo test", lines(
		"    Starting 2 tests across 1 binary",
		"        FAIL [   0.005s] foo tests::b",
		"     Summary [   0.010s] 2 tests run: 1 passed, 1 failed",
	), true)
}

func TestBodyRunnerSavings(t *testing.T) {
	var b strings.Builder
	b.WriteString("running 200 tests\n")
	for i := 0; i < 200; i++ {
		b.WriteString("test module::submodule::case_number_")
		b.WriteString(strings.Repeat("7", 1+i%4))
		b.WriteString("_")
		b.WriteString(strings.Repeat("a", 1+i%7))
		b.WriteString(" ... ok\n")
	}
	b.WriteString("\ntest result: ok. 200 passed; 0 failed; 0 ignored\n")
	raw := b.String()
	got, filtered := Body(raw, Options{Budget: 8000, Command: "cargo test"})
	if !filtered {
		t.Error("passing test names are dropped: must be marked filtered so the raw output is stored")
	}
	if want := "test result: ok. 200 passed; 0 failed; 0 ignored"; got != want {
		t.Errorf("result = %q, want %q", got, want)
	}
}
