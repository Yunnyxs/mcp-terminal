package registry

import (
	"fmt"
	"regexp"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("MCP name %q is invalid: use lowercase letters, digits, - or _ (max 32 characters, starting with a letter or digit)", name)
	}
	return nil
}
