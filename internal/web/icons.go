package web

import (
	"math"
	"strings"
)

// Icons are 16x16 and are shown at 2x (32px) so every pixel stays a whole number of screen pixels.
const iconSize = 16

// Toggle sprites are drawn on their own grids, shown at 3x (2x on small screens).
const (
	toggleTrackW = 34
	toggleTrackH = 14
	toggleThumb  = 12
)

type canvas struct {
	w, h int
	px   [][]byte
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, px: make([][]byte, h)}
	for y := range c.px {
		c.px[y] = []byte(strings.Repeat(".", w))
	}
	return c
}

func (c *canvas) at(x, y int, ch byte) *canvas {
	if x >= 0 && x < c.w && y >= 0 && y < c.h {
		c.px[y][x] = ch
	}
	return c
}

func (c *canvas) rect(x, y, w, h int, ch byte) *canvas {
	for dy := range h {
		for dx := range w {
			c.at(x+dx, y+dy, ch)
		}
	}
	return c
}

// box draws a filled rectangle with a one pixel edge.
func (c *canvas) box(x, y, w, h int, edge, fill byte) *canvas {
	return c.rect(x, y, w, h, edge).rect(x+1, y+1, w-2, h-2, fill)
}

func (c *canvas) disc(cx, cy, r float64, ch byte) *canvas {
	for y := range c.h {
		for x := range c.w {
			if math.Hypot(float64(x)-cx, float64(y)-cy) <= r {
				c.px[y][x] = ch
			}
		}
	}
	return c
}

// rounded draws a rectangle whose corners are cut in pixel steps (radius r), with a one pixel edge.
func (c *canvas) rounded(x, y, w, h, r int, edge, fill byte) *canvas {
	inset := func(row int) int { // how far the shape is pulled in, counting rows from the nearest horizontal edge
		if row >= r {
			return 0
		}
		dy := float64(r) - float64(row) - 0.5
		return int(math.Round(float64(r) - math.Sqrt(float64(r*r)-dy*dy)))
	}
	inside := func(col, row int) bool {
		if col < 0 || col >= w || row < 0 || row >= h {
			return false
		}
		in := inset(min(row, h-1-row))
		return col >= in && col < w-in
	}
	for row := range h {
		for col := range w {
			if !inside(col, row) {
				continue
			}
			ch := fill
			if !inside(col-1, row) || !inside(col+1, row) || !inside(col, row-1) || !inside(col, row+1) {
				ch = edge
			}
			c.at(x+col, y+row, ch)
		}
	}
	return c
}

func (c *canvas) line(x0, y0, x1, y1 int, ch byte) *canvas {
	dx, dy := x1-x0, y1-y0
	steps := max(abs(dx), abs(dy))
	for i := 0; i <= steps; i++ {
		c.at(x0+dx*i/max(steps, 1), y0+dy*i/max(steps, 1), ch)
	}
	return c
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (c *canvas) rows() []string {
	out := make([]string, c.h)
	for y := range c.px {
		out[y] = string(c.px[y])
	}
	return out
}

func buildIcons() map[string][]string {
	n := func() *canvas { return newCanvas(iconSize, iconSize) }

	grid := n().
		box(1, 1, 6, 6, 'i', 'g').box(9, 1, 6, 6, 'i', 'g').
		box(1, 9, 6, 6, 'i', 'g').box(9, 9, 6, 6, 'i', 'g')

	robot := n().
		rect(7, 1, 2, 2, 'i').
		box(2, 3, 12, 9, 'i', 'g').
		rect(4, 6, 2, 2, 'i').rect(10, 6, 2, 2, 'i').
		rect(5, 9, 6, 1, 'i').
		rect(3, 12, 2, 2, 'i').rect(11, 12, 2, 2, 'i').
		rect(0, 6, 2, 3, 'i').rect(14, 6, 2, 3, 'i')

	chat := n().
		box(0, 1, 11, 6, 'i', 'g').rect(2, 7, 2, 2, 'i').
		box(5, 8, 11, 6, 'i', 'b').rect(12, 14, 2, 2, 'i')

	wand := n()
	for k := range 10 {
		wand.at(2+k, 13-k, 'i').at(3+k, 13-k, 'g').at(4+k, 13-k, 'g').at(5+k, 13-k, 'i').at(3+k, 12-k, 'i').at(4+k, 12-k, 'i')
	}
	wand.rect(11, 0, 1, 5, 'y').rect(9, 2, 5, 1, 'y').rect(10, 1, 3, 3, 'y').
		rect(2, 3, 1, 1, 'y').rect(13, 9, 1, 1, 'y')

	chart := n().
		rect(0, 0, 1, 15, 'i').rect(0, 14, 16, 1, 'i').
		box(2, 9, 3, 5, 'i', 'g').box(6, 5, 3, 9, 'i', 'g').box(10, 1, 3, 13, 'i', 'g')

	gear := n().
		box(6, 0, 4, 4, 'i', 'g').box(6, 12, 4, 4, 'i', 'g').box(0, 6, 4, 4, 'i', 'g').box(12, 6, 4, 4, 'i', 'g').
		box(2, 2, 4, 4, 'i', 'g').box(10, 2, 4, 4, 'i', 'g').box(2, 10, 4, 4, 'i', 'g').box(10, 10, 4, 4, 'i', 'g').
		disc(7.5, 7.5, 5.6, 'i').disc(7.5, 7.5, 4.6, 'g').disc(7.5, 7.5, 2.6, 'i').disc(7.5, 7.5, 1.6, '.')

	cup := n().
		rect(4, 1, 1, 2, 's').rect(7, 0, 1, 3, 's').rect(10, 1, 1, 2, 's').
		box(2, 4, 10, 8, 'i', 'w').rect(3, 5, 8, 2, 'n').
		rect(12, 5, 3, 1, 'i').rect(14, 5, 1, 5, 'i').rect(12, 9, 3, 1, 'i').
		rect(1, 12, 12, 1, 'i').rect(3, 13, 8, 1, 'i')

	tooth := n().
		rect(2, 2, 12, 8, 'i').rect(3, 9, 4, 6, 'i').rect(9, 9, 4, 6, 'i').
		rect(3, 3, 10, 6, 'w').rect(4, 9, 2, 5, 'w').rect(10, 9, 2, 5, 'w').
		rect(4, 4, 2, 2, 'c')

	washer := n().
		box(2, 1, 12, 14, 'i', 'w').rect(3, 2, 10, 2, 's').rect(4, 2, 1, 1, 'r').rect(6, 2, 1, 1, 'i').
		rect(3, 4, 10, 1, 'i').
		disc(7.5, 9.5, 4.4, 'i').disc(7.5, 9.5, 3.2, 'c').rect(6, 8, 1, 1, 'w')

	key := n().
		disc(4.5, 5.5, 3.6, 'i').disc(4.5, 5.5, 2.4, 'y').disc(4.5, 5.5, 1, '.').
		rect(6, 6, 9, 3, 'i').rect(7, 7, 7, 1, 'y').
		rect(11, 9, 2, 3, 'i').rect(12, 9, 1, 2, 'y').rect(14, 9, 1, 2, 'i')

	shop := n().
		box(1, 1, 14, 5, 'i', 'r').rect(4, 2, 2, 3, 'w').rect(9, 2, 2, 3, 'w').
		box(2, 6, 12, 9, 'i', 'w').box(6, 9, 4, 6, 'i', 'n').rect(3, 8, 2, 3, 'c').rect(11, 8, 2, 3, 'c')

	sun := n().
		rect(7, 0, 2, 3, 'y').rect(7, 13, 2, 3, 'y').rect(0, 7, 3, 2, 'y').rect(13, 7, 3, 2, 'y').
		rect(2, 2, 2, 2, 'y').rect(12, 2, 2, 2, 'y').rect(2, 12, 2, 2, 'y').rect(12, 12, 2, 2, 'y').
		disc(7.5, 7.5, 4.6, 'i').disc(7.5, 7.5, 3.6, 'y')

	moon := n().
		disc(7.5, 7.5, 6.4, 'i').disc(7.5, 7.5, 5.4, 'y').
		disc(10.5, 5.5, 5.2, 'i').disc(11.5, 4.5, 4.4, '.')

	search := n().
		disc(6.5, 6.5, 5.6, 'i').disc(6.5, 6.5, 3.8, '.').
		line(10, 10, 14, 14, 'i').line(11, 10, 14, 13, 'i').line(10, 11, 13, 14, 'i')

	logout := n().
		box(1, 1, 8, 14, 'i', 'g').rect(6, 7, 1, 2, 'i').
		rect(9, 7, 6, 2, 'i').rect(12, 5, 2, 2, 'i').rect(12, 9, 2, 2, 'i').rect(14, 6, 1, 1, 'i').rect(14, 9, 1, 1, 'i')

	// The theme toggle: a pill track and a round-cornered thumb carrying the icon. Track colors are
	// t (inside), u (lit top edge), v (shaded bottom edge); thumb colors B, H (lit) and D (shaded).
	track := newCanvas(toggleTrackW, toggleTrackH).rounded(0, 0, toggleTrackW, toggleTrackH, 7, 'i', 't')
	for x := range toggleTrackW {
		if track.px[1][x] == 't' {
			track.px[1][x] = 'u'
		}
		if track.px[toggleTrackH-2][x] == 't' {
			track.px[toggleTrackH-2][x] = 'v'
		}
	}
	thumb := func() *canvas {
		t := newCanvas(toggleThumb, toggleThumb).rounded(0, 0, toggleThumb, toggleThumb, 4, 'i', 'B')
		for x := range toggleThumb {
			for _, y := range []int{toggleThumb - 3, toggleThumb - 2} {
				if t.px[y][x] == 'B' {
					t.px[y][x] = 'D'
				}
			}
			if t.px[1][x] == 'B' {
				t.px[1][x] = 'H'
			}
		}
		return t
	}
	toggleSun := thumb().disc(5.5, 5.5, 2, 'y').
		rect(5, 2, 2, 1, 'y').rect(5, 9, 2, 1, 'y').rect(2, 5, 1, 2, 'y').rect(9, 5, 1, 2, 'y').
		at(3, 3, 'y').at(8, 3, 'y').at(3, 8, 'y').at(8, 8, 'y')
	toggleMoon := thumb().disc(5, 6, 3.4, 'w').disc(7, 5, 2.9, 'B').
		at(8, 2, 'w').at(7, 3, 'w').at(8, 3, 'w').at(9, 3, 'w').at(8, 4, 'w').at(9, 7, 'w')

	return map[string][]string{
		"toggle-track":       track.rows(),
		"toggle-sun":         toggleSun.rows(),
		"toggle-moon":        toggleMoon.rows(),
		"icon-logout":        logout.rows(),
		"icon-search":        search.rows(),
		"icon-sun":           sun.rows(),
		"icon-moon":          moon.rows(),
		"icon-clients":       grid.rows(),
		"icon-agents":        robot.rows(),
		"icon-conversations": chat.rows(),
		"icon-playground":    wand.rows(),
		"icon-analytics":     chart.rows(),
		"icon-settings":      gear.rows(),
		"avatar-cup":         cup.rows(),
		"avatar-tooth":       tooth.rows(),
		"avatar-washer":      washer.rows(),
		"avatar-key":         key.rows(),
		"avatar-shop":        shop.rows(),
		"avatar-bot":         robot.rows(),
	}
}
