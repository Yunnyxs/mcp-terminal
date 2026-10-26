package compact

import (
	"fmt"
	"sort"
	"strings"
)

const (
	headKeep   = 3
	tailKeep   = 5
	markerCost = 32

	maxGapLines = 2
)

func fit(ls []line, budget int, failed bool) (string, bool) {
	n := len(ls)
	cost := func(i int) int { return len(ls[i].text) + 1 }

	total := 0
	for i := range ls {
		total += cost(i)
	}
	tailFrac := 0.5
	if failed {
		tailFrac = 0.7
	}
	if total <= budget {
		return render(ls, nil), false
	}

	keep := make([]bool, n)
	used := 0
	gaps := 1

	gapDelta := func(i int) int {
		left := i == 0 || keep[i-1]
		right := i == n-1 || keep[i+1]
		switch {
		case left && right:
			return -1
		case !left && !right:
			return 1
		}
		return 0
	}
	force := func(i int) {
		if !keep[i] {
			gaps += gapDelta(i)
			keep[i] = true
			used += cost(i)
		}
	}

	fits := func(i, limit int) bool { return used+(gaps+gapDelta(i))*markerCost+cost(i) <= limit }

	for i := n - 1; i >= max(0, n-tailKeep); i-- {
		if fits(i, budget*2/5) {
			force(i)
		}
	}
	for i := 0; i < min(headKeep, n); i++ {
		if fits(i, budget*2/5) {
			force(i)
		}
	}

	var imp []int
	for i, l := range ls {
		if l.important {
			imp = append(imp, i)
		}
	}
	sort.Slice(imp, func(a, b int) bool {
		if ls[imp[a]].score != ls[imp[b]].score {
			return ls[imp[a]].score > ls[imp[b]].score
		}
		return imp[a] > imp[b]
	})
	for _, i := range imp {
		if fits(i, budget*3/4) {
			force(i)
		}
	}

	for _, i := range imp {
		if !keep[i] {
			continue
		}
		for k := max(0, i-contextLines); k < i; k++ {
			if !keep[k] && fits(k, budget) {
				force(k)
			}
		}
		for k := i + 1; k <= min(n-1, i+trail(ls[i].text)); k++ {
			if keep[k] {
				continue
			}
			if !fits(k, budget) {
				break
			}
			force(k)
		}
	}

	if closeSmallGaps(ls, keep, budget) {
		used, gaps = 0, 0
		for i := range ls {
			if keep[i] {
				used += cost(i)
			} else if i == 0 || keep[i-1] {
				gaps++
			}
		}
	}

	if left := budget - used - gaps*markerCost; left > 0 {
		tailBudget := int(float64(left) * tailFrac)
		headBudget := left - tailBudget

		for i := 0; i < n; i++ {
			if keep[i] {
				continue
			}
			if cost(i) > headBudget {
				break
			}
			keep[i] = true
			headBudget -= cost(i)
		}

		tailBudget += headBudget
		for i := n - 1; i >= 0; i-- {
			if keep[i] {
				continue
			}
			if cost(i) > tailBudget {
				break
			}
			keep[i] = true
			tailBudget -= cost(i)
		}
	}

	closeSmallGaps(ls, keep, budget)
	return cutMiddle(render(ls, keep), budget, tailFrac), true
}

func omittedMarker(n int) string {
	if n == 1 {
		return "[... 1 line omitted ...]"
	}
	return fmt.Sprintf("[... %d lines omitted ...]", n)
}

func closeSmallGaps(ls []line, keep []bool, budget int) bool {
	type gap struct{ from, to, extra int }
	var gaps []gap
	total := 0
	for i := 0; i < len(ls); {
		if keep[i] {
			total += len(ls[i].text) + 1
			i++
			continue
		}
		j, cost := i, 0
		for j < len(ls) && !keep[j] {
			cost += len(ls[j].text) + 1
			j++
		}
		marker := len(omittedMarker(j-i)) + 1
		total += marker
		if j-i <= maxGapLines {
			gaps = append(gaps, gap{i, j, cost - marker})
		}
		i = j
	}
	sort.SliceStable(gaps, func(a, b int) bool { return gaps[a].extra < gaps[b].extra })
	closed := false
	for _, g := range gaps {
		if total+g.extra > budget {
			continue
		}
		for k := g.from; k < g.to; k++ {
			keep[k] = true
		}
		total += g.extra
		closed = true
	}
	return closed
}

func render(ls []line, keep []bool) string {
	var b strings.Builder
	for i := 0; i < len(ls); {
		if keep == nil || keep[i] {
			b.WriteString(ls[i].text)
			b.WriteByte('\n')
			i++
			continue
		}
		j := i
		for j < len(ls) && !keep[j] {
			j++
		}
		b.WriteString(omittedMarker(j - i))
		b.WriteByte('\n')
		i = j
	}
	return strings.TrimRight(b.String(), "\n")
}

func cutMiddle(s string, budget int, tailFrac float64) string {
	if len(s) <= budget {
		return s
	}
	const marker = "\n[... truncated ...]\n"
	room := budget - len(marker)
	if room <= 0 {
		return prefix(s, budget)
	}
	t := int(float64(room) * tailFrac)
	return prefix(s, room-t) + marker + suffix(s, t)
}
