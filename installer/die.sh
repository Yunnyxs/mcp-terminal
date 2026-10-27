die() { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }
