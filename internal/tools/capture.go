package tools

import (
	"fmt"
	"strings"
)

type capture struct {
	head, tail []byte
	half       int
	dropped    int64
}

func newCapture(max int) *capture { return &capture{half: max / 2} }

func (c *capture) Write(p []byte) (int, error) {
	n := len(p)
	if room := c.half - len(c.head); room > 0 {
		k := min(room, len(p))
		c.head = append(c.head, p[:k]...)
		p = p[k:]
	}
	c.tail = append(c.tail, p...)
	if len(c.tail) > 2*c.half {
		over := len(c.tail) - c.half
		c.dropped += int64(over)
		c.tail = append([]byte(nil), c.tail[over:]...)
	}
	return n, nil
}

func (c *capture) String() string {
	tail, dropped := c.tail, c.dropped
	if len(tail) > c.half {
		dropped += int64(len(tail) - c.half)
		tail = tail[len(tail)-c.half:]
	}
	s := string(c.head)
	if dropped > 0 {
		s += fmt.Sprintf("\n...[%d bytes truncated]...\n", dropped)
	}

	return strings.ToValidUTF8(s+string(tail), "\uFFFD")
}
