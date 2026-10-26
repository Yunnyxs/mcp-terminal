package clierr

import (
	"errors"
	"fmt"
)

type Usage string

func (u Usage) Error() string { return string(u) }

func Usagef(format string, a ...any) error { return Usage(fmt.Sprintf(format, a...)) }

var Reported = errors.New("reported")
