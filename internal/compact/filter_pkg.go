package compact

import "regexp"

var (
	rePip = regexp.MustCompile(`^pip[0-9.]*$`)

	pipFilter = Filter{
		Name: "pip",
		Match: func(args []string) bool {
			t, rest := toolOf(args)
			if t == "uv" && len(rest) > 0 && rest[0] == "pip" {
				t, rest = "pip", rest[1:]
			}
			if !rePip.MatchString(t) {
				return false
			}
			sub, _ := firstNonFlag(rest, "--proxy", "--retries", "--timeout", "--exists-action", "--trusted-host",
				"--cert", "--client-cert", "--cache-dir", "--log", "--python", "-i", "--index-url")
			return isOneOf(sub, "install", "uninstall", "download", "wheel", "sync")
		},
		Apply: rulesFilter([]dropRule{
			boiler(`[━█▉▊▋▌▍▎▏╸╺]{3,}`),
			boiler(`^\s*\[notice\]`),
			boiler(`^\s*(Collecting|Downloading|Using cached|Obtaining|Preparing metadata|Getting requirements|Installing build dependencies|Installing backend dependencies|Building wheels? for|Building editable|Preparing editable|Created wheel|Stored in directory|Running setup\.py|Running command|Attempting uninstall|Found existing installation|Uninstalling|Installing collected packages)\b`),
			countDrop(`^Requirement already satisfied`, "requirement already satisfied", "requirements already satisfied"),
		}),
	}

	nodeInstallFilter = Filter{
		Name: "npm/yarn/pnpm install",
		Match: func(args []string) bool {
			t, rest := toolOf(args)
			if !isOneOf(t, "npm", "yarn", "pnpm", "bun") {
				return false
			}
			sub, _ := firstNonFlag(rest, "--prefix", "-C", "--cwd", "--filter", "-F", "-w", "--workspace")
			if t == "yarn" && sub == "" {
				return !hasAny(rest, "--version", "-v", "--help", "-h")
			}
			return isOneOf(sub, "install", "i", "add", "ci", "update", "up", "upgrade", "uninstall", "remove", "rm", "un", "dedupe", "prune")
		},
		Apply: rulesFilter([]dropRule{
			boiler(`^npm (notice|http)\b`),
			boiler(`^\d+ packages? (is|are) looking for funding`),
			boiler("^\\s*run `npm fund` for details"),
			boiler(`^(yarn|bun) (install|add|remove|upgrade) v\d`),
			boiler(`^\[\d/\d\] `),
			boiler(`^success Saved lockfile`),
			boiler(`^Progress: resolved `),
			boiler(`^Packages: [+-]`),
			boiler(`^[+-]{5,}$`),
		}),
	}

	aptFilter = Filter{
		Name: "apt",
		Match: func(args []string) bool {
			t, rest := toolOf(args)
			if !isOneOf(t, "apt", "apt-get") {
				return false
			}
			sub, _ := firstNonFlag(rest, "-o", "--option", "-c", "--config-file", "-t", "--target-release", "-a", "--host-architecture")
			return isOneOf(sub, "install", "update", "upgrade", "dist-upgrade", "full-upgrade", "remove", "purge", "autoremove", "autopurge", "reinstall")
		},
		Apply: rulesFilter([]dropRule{
			boiler(`^(Reading package lists|Building dependency tree|Reading state information)`),
			boiler(`^\(Reading database`),
			boiler(`^(Selecting previously unselected package|Preparing to unpack|Unpacking|Processing triggers for|Extracting templates from packages)`),
			boiler(`^(debconf|dpkg-preconfigure): `),
			countDrop(`^Get:\d+ `, "item fetched", "items fetched"),
			countDrop(`^Hit:\d+ `, "source up to date", "sources up to date"),
			countDrop(`^Ign:\d+ `, "source ignored", "sources ignored"),
			countDrop(`^Setting up `, "package set up", "packages set up"),
			countDrop(`^Removing `, "package removed", "packages removed"),
		}),
	}
)
