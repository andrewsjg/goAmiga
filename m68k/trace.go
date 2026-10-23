package m68k

import (
	"bytes"
	"fmt"
)

const noSize = uint16(0xFFFF)

type stepTrace struct {
	addr         uint32
	op, src, dst string
	sz           uint16
	n            int // cycle count
	err          error
}

func (c *stepTrace) String() string {
	b := &bytes.Buffer{}
	fmt.Fprintf(b, "%08X %s", c.addr, c.op)
	if c.sz != noSize {
		b.WriteByte('.')
		// SizeByte/Word/Long are 0/1/2; illegal encodings must not panic.
		switch c.sz {
		case SizeByte:
			b.WriteByte('b')
		case SizeWord:
			b.WriteByte('w')
		case SizeLong:
			b.WriteByte('l')
		default:
			fmt.Fprintf(b, "?%d", c.sz)
		}
	}
	operands := c.dst
	if c.dst == "" {
		operands = c.src
	} else if c.src != "" {
		operands = fmt.Sprintf("%s,%s", c.src, c.dst)
	}
	if operands != "" {
		b.WriteByte(' ')
		b.WriteString(operands)
	}
	return b.String()
}

// sprintf formats a trace's text, only while tracing: the instructions
// build their operands' text every time they run, and formatting it for
// nobody cost a third of the emulator's time.
func (c *Processor) sprintf(format string, a ...any) string {
	if c.TraceWriter == nil {
		return ""
	}
	return fmt.Sprintf(format, a...)
}
