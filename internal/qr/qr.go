// Package qr is a small, dependency-free QR Code encoder (byte mode, error
// correction level M) that renders to SVG. It follows ISO/IEC 18004 and the
// structure of Project Nayuki's reference implementation.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

var eccPerBlockM = [41]int{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28}
var numBlocksM = [41]int{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49}

// Code is an encoded QR symbol; Modules[y][x] is true for dark modules.
type Code struct {
	Version int
	Size    int
	Modules [][]bool
	fn      [][]bool
}

func rawModules(v int) int {
	r := (16*v+128)*v + 64
	if v >= 2 {
		n := v/7 + 2
		r -= (25*n-10)*n - 55
		if v >= 7 {
			r -= 36
		}
	}
	return r
}

func dataCodewords(v int) int { return rawModules(v)/8 - eccPerBlockM[v]*numBlocksM[v] }

// Encode encodes data as a QR Code (level M), choosing the smallest version.
func Encode(data []byte) (*Code, error) {
	ver := 0
	for v := 1; v <= 40; v++ {
		cc := 8
		if v >= 10 {
			cc = 16
		}
		if 4+cc+len(data)*8 <= dataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, errors.New("qr: data too long")
	}
	cc := 8
	if ver >= 10 {
		cc = 16
	}
	var bits []bool
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>i)&1 == 1)
		}
	}
	put(4, 4)
	put(len(data), cc)
	for _, b := range data {
		put(int(b), 8)
	}
	capBits := dataCodewords(ver) * 8
	t := capBits - len(bits)
	if t > 4 {
		t = 4
	}
	put(0, t)
	put(0, (8-len(bits)%8)%8)
	for pad := 0xEC; len(bits) < capBits; pad ^= 0xEC ^ 0x11 {
		put(pad, 8)
	}
	cw := make([]byte, len(bits)/8)
	for i, b := range bits {
		if b {
			cw[i>>3] |= 1 << (7 - uint(i&7))
		}
	}
	c := &Code{Version: ver, Size: ver*4 + 17}
	c.Modules, c.fn = grid(c.Size), grid(c.Size)
	c.drawFunctionPatterns()
	c.drawCodewords(addECC(cw, ver))
	best, bestP := 0, -1
	for m := 0; m < 8; m++ {
		c.applyMask(m)
		c.drawFormat(m)
		if p := c.penalty(); bestP < 0 || p < bestP {
			best, bestP = m, p
		}
		c.applyMask(m)
	}
	c.applyMask(best)
	c.drawFormat(best)
	c.fn = nil
	return c, nil
}

func grid(n int) [][]bool {
	g := make([][]bool, n)
	for i := range g {
		g[i] = make([]bool, n)
	}
	return g
}

func (c *Code) set(x, y int, dark bool) { c.Modules[y][x], c.fn[y][x] = dark, true }

func (c *Code) drawFunctionPatterns() {
	for i := 0; i < c.Size; i++ {
		c.set(6, i, i%2 == 0)
		c.set(i, 6, i%2 == 0)
	}
	c.finder(3, 3)
	c.finder(c.Size-4, 3)
	c.finder(3, c.Size-4)
	pos := c.alignPositions()
	n := len(pos)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == 0 && j == 0 || i == 0 && j == n-1 || i == n-1 && j == 0 {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					c.set(pos[i]+dx, pos[j]+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	c.drawFormat(0)
	if c.Version >= 7 {
		rem := c.Version
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		b := c.Version<<12 | rem
		for i := 0; i < 18; i++ {
			bit := (b>>i)&1 == 1
			a, bb := c.Size-11+i%3, i/3
			c.set(a, bb, bit)
			c.set(bb, a, bit)
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func (c *Code) finder(x, y int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			xx, yy := x+dx, y+dy
			if xx >= 0 && xx < c.Size && yy >= 0 && yy < c.Size {
				d := max(abs(dx), abs(dy))
				c.set(xx, yy, d != 2 && d != 4)
			}
		}
	}
}

func (c *Code) alignPositions() []int {
	if c.Version == 1 {
		return nil
	}
	n := c.Version/7 + 2
	step := (c.Version*4 + n*2 + 1) / (n*2 - 2) * 2
	if c.Version == 32 {
		step = 26
	}
	res := make([]int, n)
	res[0] = 6
	for i, p := n-1, c.Size-7; i >= 1; i, p = i-1, p-step {
		res[i] = p
	}
	return res
}

func (c *Code) drawFormat(mask int) {
	data := 0<<3 | mask // level M = 00
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	b := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (b>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		c.set(8, i, bit(i))
	}
	c.set(8, 7, bit(6))
	c.set(8, 8, bit(7))
	c.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		c.set(c.Size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.set(8, c.Size-15+i, bit(i))
	}
	c.set(8, c.Size-8, true)
}

func gfMul(x, y int) int {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= ((y >> i) & 1) * x
	}
	return z & 0xFF
}

func rsDivisor(deg int) []int {
	r := make([]int, deg)
	r[deg-1] = 1
	root := 1
	for i := 0; i < deg; i++ {
		for j := range r {
			r[j] = gfMul(r[j], root)
			if j+1 < len(r) {
				r[j] ^= r[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	return r
}

func rsRemainder(data []byte, div []int) []byte {
	r := make([]int, len(div))
	for _, b := range data {
		f := int(b) ^ r[0]
		copy(r, r[1:])
		r[len(r)-1] = 0
		for i := range r {
			r[i] ^= gfMul(div[i], f)
		}
	}
	out := make([]byte, len(r))
	for i, v := range r {
		out[i] = byte(v)
	}
	return out
}

func addECC(data []byte, ver int) []byte {
	nb, el := numBlocksM[ver], eccPerBlockM[ver]
	raw := rawModules(ver) / 8
	nShort := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(el)
	var blocks [][]byte
	k := 0
	for i := 0; i < nb; i++ {
		dl := shortLen - el
		if i >= nShort {
			dl++
		}
		dat := append([]byte(nil), data[k:k+dl]...)
		k += dl
		ecc := rsRemainder(dat, div)
		if i < nShort {
			dat = append(dat, 0)
		}
		blocks = append(blocks, append(dat, ecc...))
	}
	var out []byte
	for i := range blocks[0] {
		for j, b := range blocks {
			if i != shortLen-el || j >= nShort {
				out = append(out, b[i])
			}
		}
	}
	return out
}

func (c *Code) drawCodewords(data []byte) {
	i := 0
	for right := c.Size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < c.Size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = c.Size - 1 - vert
				}
				if !c.fn[y][x] && i < len(data)*8 {
					c.Modules[y][x] = (data[i>>3]>>(7-uint(i&7)))&1 == 1
					i++
				}
			}
		}
	}
}

func (c *Code) applyMask(m int) {
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			var inv bool
			switch m {
			case 0:
				inv = (x+y)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (x+y)%3 == 0
			case 4:
				inv = (x/3+y/2)%2 == 0
			case 5:
				inv = x*y%2+x*y%3 == 0
			case 6:
				inv = (x*y%2+x*y%3)%2 == 0
			case 7:
				inv = ((x+y)%2+x*y%3)%2 == 0
			}
			if inv && !c.fn[y][x] {
				c.Modules[y][x] = !c.Modules[y][x]
			}
		}
	}
}

func (c *Code) penalty() int {
	n, p := c.Size, 0
	at := func(x, y int, col bool) bool {
		if col {
			return c.Modules[x][y]
		}
		return c.Modules[y][x]
	}
	for _, col := range []bool{false, true} {
		for y := 0; y < n; y++ {
			run := 1
			for x := 1; x <= n; x++ {
				if x < n && at(x, y, col) == at(x-1, y, col) {
					run++
					continue
				}
				if run >= 5 {
					p += 3 + run - 5
				}
				run = 1
			}
			for x := 0; x+11 <= n; x++ { // finder-like 1011101 with 4 light modules on one side
				a := [11]bool{}
				for k := 0; k < 11; k++ {
					a[k] = at(x+k, y, col)
				}
				pat := [7]bool{true, false, true, true, true, false, true}
				m1, m2 := true, true
				for k := 0; k < 7; k++ {
					m1 = m1 && a[k] == pat[k]
					m2 = m2 && a[k+4] == pat[k]
				}
				m1 = m1 && !a[7] && !a[8] && !a[9] && !a[10]
				m2 = m2 && !a[0] && !a[1] && !a[2] && !a[3]
				if m1 {
					p += 40
				}
				if m2 {
					p += 40
				}
			}
		}
	}
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if c.Modules[y][x] {
				dark++
			}
			if x+1 < n && y+1 < n {
				v := c.Modules[y][x]
				if c.Modules[y][x+1] == v && c.Modules[y+1][x] == v && c.Modules[y+1][x+1] == v {
					p += 3
				}
			}
		}
	}
	total := n * n
	k := (abs(dark*20-total*10)+total-1)/total - 1
	return p + k*10
}

// SVG renders the code with a 4-module quiet zone. Colors are CSS colors.
func (c *Code) SVG(fg, bg string) string {
	q := 4
	sz := c.Size + 2*q
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, sz, sz)
	if bg != "" {
		fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="%s"/>`, sz, sz, bg)
	}
	fmt.Fprintf(&b, `<path fill="%s" d="`, fg)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Modules[y][x] {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+q, y+q)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}
