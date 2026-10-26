package compact

import (
	"strconv"
	"strings"
	"testing"
)

func TestPkgFiltersNotMatched(t *testing.T) {
	for _, cmd := range []string{
		"pip list", "pip freeze", "pip --version", "pip show requests",
		"npm run build", "yarn --version", "npm --version",
		"apt search curl", "apt list --installed", "apt-cache policy curl",
	} {
		if f := lookupFilter(cmd); f != nil && (f.Name == "pip" || f.Name == "apt" || f.Name == "npm/yarn/pnpm install") {
			t.Errorf("lookupFilter(%q) = %s, want not a package manager filter", cmd, f.Name)
		}
	}
}

func TestPipFilterInstall(t *testing.T) {
	in := lines(
		"Collecting requests",
		"  Downloading requests-2.31.0-py3-none-any.whl (62 kB)",
		"     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 62.6/62.6 kB 1.2 MB/s eta 0:00:00",
		"Requirement already satisfied: idna<4,>=2.5 in /usr/lib/python3/dist-packages (from requests) (3.6)",
		"Requirement already satisfied: urllib3<3,>=1.21.1 in /usr/lib/python3/dist-packages (from requests) (2.0.7)",
		"Installing collected packages: requests",
		"Successfully installed requests-2.31.0",
		"",
		"[notice] A new release of pip is available: 23.0 -> 24.0",
		"[notice] To update, run: pip install --upgrade pip",
	)
	want := lines(
		"Successfully installed requests-2.31.0",
		"",
		"[2 requirements already satisfied]",
	)
	wantOut(t, "pip install requests", in, "pip", want, true)
	wantOut(t, "python3 -m pip install requests", in, "pip", want, true)
	wantOut(t, "uv pip install requests", in, "pip", want, true)
}

func TestPipFilterOnlyBoilerplateIsNotLossy(t *testing.T) {
	in := lines(
		"Collecting requests",
		"  Using cached requests-2.31.0-py3-none-any.whl (62 kB)",
		"Installing collected packages: requests",
		"Successfully installed requests-2.31.0",
	)
	wantOut(t, "pip install requests", in, "pip", "Successfully installed requests-2.31.0", false)
}

func TestPipFilterKeepsErrorsAndWarnings(t *testing.T) {
	in := lines(
		"Collecting nonexistent-pkg",
		"WARNING: Retrying (Retry(total=4)) after connection broken by 'ReadTimeoutError'",
		"ERROR: Could not find a version that satisfies the requirement nonexistent-pkg (from versions: none)",
		"ERROR: No matching distribution found for nonexistent-pkg",
	)
	want := lines(
		"WARNING: Retrying (Retry(total=4)) after connection broken by 'ReadTimeoutError'",
		"ERROR: Could not find a version that satisfies the requirement nonexistent-pkg (from versions: none)",
		"ERROR: No matching distribution found for nonexistent-pkg",
	)
	wantOut(t, "pip install nonexistent-pkg", in, "pip", want, false)
}

func TestPkgFiltersGoldenRuleBeatsDropRules(t *testing.T) {
	wantOut(t, "pip install fail2ban", lines("Collecting fail2ban", "Successfully installed fail2ban-1.0"),
		"pip", lines("Collecting fail2ban", "Successfully installed fail2ban-1.0"), false)
	wantOut(t, "apt-get install -y x", lines("Get:1 http://m/x amd64 x [1 kB]", "Ign:2 http://m/y InRelease  Temporary failure resolving 'm'"),
		"apt", lines("Ign:2 http://m/y InRelease  Temporary failure resolving 'm'", "[1 item fetched]"), true)
}

func TestNodeInstallNpm(t *testing.T) {
	in := lines(
		"npm http fetch GET 200 https://registry.npmjs.org/left-pad 120ms",
		"npm notice created a lockfile as package-lock.json",
		"added 57 packages, and audited 58 packages in 3s",
		"",
		"8 packages are looking for funding",
		"  run `npm fund` for details",
		"",
		"found 0 vulnerabilities",
	)
	want := lines(
		"added 57 packages, and audited 58 packages in 3s",
		"",
		"found 0 vulnerabilities",
	)
	wantOut(t, "npm install", in, "npm/yarn/pnpm install", want, false)
	wantOut(t, "npm ci", in, "npm/yarn/pnpm install", want, false)
	wantOut(t, "npm i left-pad", in, "npm/yarn/pnpm install", want, false)
}

func TestNodeInstallNpmKeepsWarnAndErr(t *testing.T) {
	in := lines(
		"npm WARN deprecated request@2.88.2: request has been deprecated",
		"npm notice New major version of npm available",
		"npm ERR! code E404",
		"npm ERR! 404 Not Found - GET https://registry.npmjs.org/nope",
	)
	want := lines(
		"npm WARN deprecated request@2.88.2: request has been deprecated",
		"npm ERR! code E404",
		"npm ERR! 404 Not Found - GET https://registry.npmjs.org/nope",
	)
	wantOut(t, "npm install nope", in, "npm/yarn/pnpm install", want, false)
}

func TestNodeInstallYarnClassic(t *testing.T) {
	in := lines(
		"yarn add v1.22.19",
		"[1/4] Resolving packages...",
		"[2/4] Fetching packages...",
		"[3/4] Linking dependencies...",
		"[4/4] Building fresh packages...",
		"success Saved lockfile.",
		"success Saved 1 new dependency.",
		"info Direct dependencies",
		"└─ left-pad@1.3.0",
		"Done in 1.2s.",
	)
	want := lines(
		"success Saved 1 new dependency.",
		"info Direct dependencies",
		"└─ left-pad@1.3.0",
		"Done in 1.2s.",
	)
	wantOut(t, "yarn add left-pad", in, "npm/yarn/pnpm install", want, false)

	wantOut(t, "yarn", in, "npm/yarn/pnpm install", want, false)
}

func TestNodeInstallPnpm(t *testing.T) {
	in := lines(
		"Progress: resolved 1, reused 0, downloaded 0, added 0",
		"Packages: +1",
		"++++++++",
		"Progress: resolved 1, reused 1, downloaded 0, added 1, done",
		"dependencies:",
		"+ left-pad 1.3.0",
	)
	want := lines("dependencies:", "+ left-pad 1.3.0")
	wantOut(t, "pnpm add left-pad", in, "npm/yarn/pnpm install", want, false)
}

func TestAptFilterInstall(t *testing.T) {
	in := lines(
		"Reading package lists... Done",
		"Building dependency tree... Done",
		"Reading state information... Done",
		"The following NEW packages will be installed:",
		"  curl",
		"0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.",
		"Need to get 194 kB of archives.",
		"Get:1 http://archive.ubuntu.com/ubuntu noble/main amd64 curl amd64 8.5.0 [194 kB]",
		"Fetched 194 kB in 1s (200 kB/s)",
		"Selecting previously unselected package curl.",
		"(Reading database ... 52000 files and directories currently installed.)",
		"Preparing to unpack .../curl_8.5.0_amd64.deb ...",
		"Unpacking curl (8.5.0) ...",
		"Setting up curl (8.5.0) ...",
		"Processing triggers for man-db (2.12.0-4build2) ...",
	)
	want := lines(
		"The following NEW packages will be installed:",
		"  curl",
		"0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.",
		"Need to get 194 kB of archives.",
		"Fetched 194 kB in 1s (200 kB/s)",
		"[1 item fetched]",
		"[1 package set up]",
	)
	wantOut(t, "apt install curl", in, "apt", want, true)
	wantOut(t, "sudo apt-get install -y curl", in, "apt", want, true)
}

func TestAptFilterUpdateCounts(t *testing.T) {
	in := lines(
		"Hit:1 http://archive.ubuntu.com/ubuntu noble InRelease",
		"Hit:2 http://archive.ubuntu.com/ubuntu noble-updates InRelease",
		"Get:3 http://security.ubuntu.com/ubuntu noble-security InRelease [126 kB]",
		"Ign:4 http://ppa.example.com/ubuntu noble InRelease",
		"Fetched 126 kB in 1s (120 kB/s)",
		"Reading package lists... Done",
		"All packages are up to date.",
	)
	want := lines(
		"Fetched 126 kB in 1s (120 kB/s)",
		"All packages are up to date.",
		"[1 item fetched]",
		"[2 sources up to date]",
		"[1 source ignored]",
	)
	wantOut(t, "apt update", in, "apt", want, true)
}

func TestAptFilterKeepsErrorsAndWarnings(t *testing.T) {
	in := lines(
		"Reading package lists... Done",
		"E: Failed to fetch http://archive.ubuntu.com/ubuntu/pool/main/c/curl/curl.deb  404  Not Found",
		"E: Unable to locate package nopkg",
		"W: Some index files failed to download. They have been ignored, or old ones used instead.",
		"E: Sub-process /usr/bin/dpkg returned an error code (1)",
	)
	want := lines(
		"E: Failed to fetch http://archive.ubuntu.com/ubuntu/pool/main/c/curl/curl.deb  404  Not Found",
		"E: Unable to locate package nopkg",
		"W: Some index files failed to download. They have been ignored, or old ones used instead.",
		"E: Sub-process /usr/bin/dpkg returned an error code (1)",
	)
	wantOut(t, "apt-get install nopkg", in, "apt", want, false)
}

func TestAptFilterRemoveCountsPackages(t *testing.T) {
	in := lines(
		"The following packages will be REMOVED:",
		"  curl",
		"Removing curl (8.5.0) ...",
		"Processing triggers for man-db (2.12.0-4build2) ...",
	)
	want := lines("The following packages will be REMOVED:", "  curl", "[1 package removed]")
	wantOut(t, "apt remove curl", in, "apt", want, true)
}

func TestBodyAptManyFetchesKeepsFailure(t *testing.T) {
	var ls []string
	for i := 1; i <= 30; i++ {
		ls = append(ls, "Get:"+strconv.Itoa(i)+" http://archive.ubuntu.com/ubuntu noble/main amd64 pkg"+strconv.Itoa(i)+" amd64 1.0 [10 kB]")
	}
	ls = append(ls, "E: Failed to fetch http://archive.ubuntu.com/ubuntu/pool/x.deb  404  Not Found")
	got, lossy := Body(lines(ls...), Options{Budget: 10000, Failed: true, Command: "apt-get install -y x"})
	if !lossy {
		t.Error("lossy = false, want true (30 Get lines were summarized)")
	}
	if !strings.Contains(got, "[30 items fetched]") {
		t.Errorf("the Get count is missing:\n%s", got)
	}
	if !strings.Contains(got, "E: Failed to fetch") {
		t.Errorf("the error line is missing:\n%s", got)
	}
	if strings.Contains(got, "pkg17") {
		t.Errorf("the Get lines were not dropped:\n%s", got)
	}
}
