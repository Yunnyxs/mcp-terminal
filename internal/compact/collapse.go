package compact

import "fmt"

const (
	minRun = 4

	minRunImportant = 8

	keepFirstImportant = 2
)

func collapse(ls []line, similar bool) ([]line, bool) {
	same := func(a, b line) bool { return a.key == b.key }
	if similar {
		same = func(a, b line) bool { return a.shape == b.shape }
	}
	var out []line
	lossy := false
	for i := 0; i < len(ls); {
		j := i + 1
		for j < len(ls) && same(ls[i], ls[j]) {
			j++
		}
		n := j - i
		switch {
		case n > 1 && allSameText(ls[i:j]):

			l := ls[i]
			l.text += fmt.Sprintf(" (x%d)", n)
			out = append(out, l)
		case similar && n >= minRun && !ls[i].important:

			out = append(out, ls[i], line{text: fmt.Sprintf("[+%d similar]", n-2)}, ls[j-1])
			lossy = true
		case similar && n >= minRunImportant && ls[i].important:

			out = append(out, ls[i:i+keepFirstImportant]...)
			out = append(out, line{text: fmt.Sprintf("[+%d similar]", n-keepFirstImportant-1)}, ls[j-1])
			lossy = true
		default:
			out = append(out, ls[i:j]...)
		}
		i = j
	}
	return out, lossy
}

func allSameText(ls []line) bool {
	for _, l := range ls[1:] {
		if l.text != ls[0].text {
			return false
		}
	}
	return true
}

func size(ls []line) int {
	n := 0
	for _, l := range ls {
		n += len(l.text) + 1
	}
	return n
}

func anyCut(ls []line) bool {
	for _, l := range ls {
		if l.cut {
			return true
		}
	}
	return false
}
