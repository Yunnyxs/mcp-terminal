package compact

import (
	"fmt"
	"strings"
	"time"
)

const slowAfter = 5 * time.Second

type Options struct {
	Budget  int
	Failed  bool
	Command string
}

func Body(raw string, o Options) (string, bool) {
	lines := clean(raw)
	filtered := false
	if f := lookupFilter(o.Command); f != nil {
		fl, lossy := f.Apply(lines)
		switch {
		case !lossy && byteSize(fl) <= byteSize(lines):
			lines = fl
		case byteSize(lines)-byteSize(fl) >= minFilterGain:
			lines, filtered = fl, true
		}
	}
	all := analyze(lines)

	ls, _ := collapse(all, false)
	lossy := false
	if size(ls) > o.Budget {
		ls, lossy = collapse(all, true)
	}
	body, cut := fit(ls, o.Budget, o.Failed)

	return body, lossy || cut || anyCut(ls) || filtered
}

func Status(exit string, dur time.Duration) string {
	var parts []string
	if exit != "0" {
		parts = append(parts, "exit "+exit)
	}
	if dur > slowAfter {
		parts = append(parts, fmt.Sprintf("(%ds)", int(dur.Seconds()+0.5)))
	}
	return strings.Join(parts, " ")
}

func Join(status, body string) string {
	switch {
	case status == "" && body == "":
		return "ok"
	case body == "":
		return status
	case status == "":
		return body
	}
	return status + "\n" + body
}
