package lha

// The -lh5-/-lh6-/-lh7- decoder: LZSS whose literals and match lengths
// share one Huffman code (NC symbols: 256 literals, then lengths from 3)
// and whose distances have another, both sent per block with their code
// lengths, themselves Huffman coded (the "pt" code). As in Haruhiko
// Okumura's ar002, which LhA is built on.

const (
	nc        = 256 + 256 - 3 + 1 // literals and lengths 3-256
	nt        = 19                // the code-length code
	tbit      = 5
	cbit      = 9
	threshold = 3
)

// bitReader reads bits most significant first; past the end it reads 0.
type bitReader struct {
	b   []byte
	pos int // in bits
}

func (r *bitReader) bits(n int) int {
	v := 0
	for range n {
		v <<= 1
		if i := r.pos >> 3; i < len(r.b) && r.b[i]&(0x80>>(r.pos&7)) != 0 {
			v |= 1
		}
		r.pos++
	}
	return v
}

func (r *bitReader) peek(n int) int {
	p := r.pos
	v := r.bits(n)
	r.pos = p
	return v
}

// huffman is a canonical code: symbols by length, then by value.
type huffman struct {
	single   int // the only symbol, coded with no bits (-1: none)
	count    [17]int
	first    [17]int // first code of each length
	offset   [17]int // index in syms of each length's first symbol
	syms     []int
	maxLen   int
	nonEmpty bool
}

func newHuffman(lens []int) *huffman {
	h := &huffman{single: -1}
	for _, l := range lens {
		if l > 0 && l <= 16 {
			h.count[l]++
			h.nonEmpty = true
			h.maxLen = max(h.maxLen, l)
		}
	}
	code, idx := 0, 0
	for l := 1; l <= 16; l++ {
		h.first[l], h.offset[l] = code, idx
		idx += h.count[l]
		code = (code + h.count[l]) << 1
	}
	h.syms = make([]int, idx)
	fill := h.offset
	for s, l := range lens {
		if l > 0 && l <= 16 {
			h.syms[fill[l]] = s
			fill[l]++
		}
	}
	return h
}

func constant(sym int) *huffman { return &huffman{single: sym} }

func (h *huffman) decode(r *bitReader) int {
	if h.single >= 0 {
		return h.single
	}
	code := 0
	for l := 1; l <= h.maxLen; l++ {
		code = code<<1 | r.bits(1)
		if i := code - h.first[l]; i >= 0 && i < h.count[l] {
			return h.syms[h.offset[l]+i]
		}
	}
	return 0 // a corrupt stream; the length check will catch it
}

// readPTLen reads the lengths of the code-length code (nn symbols, n in
// nbit bits) or of the distance code. After the third length of the
// code-length code (special), 2 bits give a run of zero lengths.
func readPTLen(r *bitReader, nn, nbit, special int) *huffman {
	n := r.bits(nbit)
	if n == 0 {
		return constant(r.bits(nbit))
	}
	lens := make([]int, max(nn, n))
	for i := 0; i < n; {
		c := r.peek(3)
		if c == 7 {
			r.bits(3)
			for r.bits(1) == 1 {
				c++
			}
		} else {
			r.bits(3)
		}
		lens[i] = c
		i++
		if i == special {
			for z := r.bits(2); z > 0 && i < len(lens); z-- {
				lens[i] = 0
				i++
			}
		}
	}
	return newHuffman(lens[:nn])
}

// readCLen reads the lengths of the literal and length code, coded with
// the code-length code pt.
func readCLen(r *bitReader, pt *huffman) *huffman {
	n := r.bits(cbit)
	if n == 0 {
		return constant(r.bits(cbit))
	}
	lens := make([]int, nc)
	for i := 0; i < n && i < nc; {
		c := pt.decode(r)
		if c <= 2 {
			run := 1
			switch c {
			case 1:
				run = r.bits(4) + 3
			case 2:
				run = r.bits(cbit) + 20
			}
			for ; run > 0 && i < nc; run-- {
				lens[i] = 0
				i++
			}
		} else {
			lens[i] = c - 2
			i++
		}
	}
	return newHuffman(lens)
}

// decode unpacks size bytes coded with a 2^dicbit-byte window, distances
// coded in pbit-bit counts.
func decode(packed []byte, size, dicbit, pbit int) []byte {
	r := &bitReader{b: packed}
	np := dicbit + 1
	out := make([]byte, 0, size)
	blockLeft := 0
	var c, p *huffman
	for len(out) < size {
		if blockLeft == 0 {
			blockLeft = r.bits(16)
			if blockLeft == 0 && r.pos>>3 >= len(packed) {
				break
			}
			pt := readPTLen(r, nt, tbit, 3)
			c = readCLen(r, pt)
			p = readPTLen(r, np, pbit, -1)
		}
		blockLeft--
		sym := c.decode(r)
		if sym < 256 {
			out = append(out, byte(sym))
			continue
		}
		length := sym - 256 + threshold
		d := p.decode(r)
		if d != 0 {
			d = 1<<(d-1) + r.bits(d-1)
		}
		from := len(out) - d - 1
		for k := 0; k < length && len(out) < size; k++ {
			if from+k < 0 {
				out = append(out, 0) // before the start: the window starts blank
			} else {
				out = append(out, out[from+k])
			}
		}
	}
	return out
}
