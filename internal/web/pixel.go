package web

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Sprites are ASCII grids: one character per pixel, '.' is transparent. Each character maps to a
// CSS class (.p-<char> in app.css), so colors follow the light and dark themes.
//
//	i  theme ink (outlines that flip in dark mode)   k  fixed dark green outline
//	g  green   l  light green   d  dark green
//	w  white   p  pink   b  blue   y  yellow   r  red   n  brown   c  cyan   s  grey
//	t  toggle track   u  its lit edge   v  its shaded edge   B  toggle thumb   H  its lit edge   D  its shaded edge

const (
	mascotW = 26
	mascotH = 22
)

var sprites = map[string][]string{}

func init() {
	for name, expr := range map[string]mascotExpression{
		"mascot":        {eyes: eyesOpen, mouth: mouthSmile},
		"mascot-wave":   {eyes: eyesOpen, mouth: mouthSmile, wave: true},
		"mascot-cover":  {eyes: eyesNone, mouth: mouthFlat, cover: true},
		"mascot-worry":  {eyes: eyesWorried, mouth: mouthWobble, sweat: true},
		"mascot-think":  {eyes: eyesUp, mouth: mouthO, think: true},
		"mascot-sleepy": {eyes: eyesClosed, mouth: mouthSmile},
	} {
		sprites[name] = drawMascot(expr)
	}
	for name, art := range buildIcons() {
		sprites[name] = art
	}
}

type (
	eyeStyle         int
	mouthStyle       int
	mascotExpression struct {
		eyes  eyeStyle
		mouth mouthStyle
		wave  bool // raised arm
		cover bool // both hands over the eyes
		think bool // hand on chin and a question mark
		sweat bool
	}
)

const (
	eyesOpen eyeStyle = iota
	eyesClosed
	eyesWorried
	eyesUp
	eyesNone
)

const (
	mouthSmile mouthStyle = iota
	mouthFlat
	mouthWobble
	mouthO
)

// drawMascot builds the mascot: a rounded sprout-blob with a leaf, whose face and arms
// depend on the expression. The body is computed so every variant shares the same silhouette.
func drawMascot(e mascotExpression) []string {
	g := make([][]byte, mascotH)
	for y := range g {
		g[y] = []byte(strings.Repeat(".", mascotW))
	}
	set := func(x, y int, c byte) {
		if x >= 0 && x < mascotW && y >= 0 && y < mascotH {
			g[y][x] = c
		}
	}
	put := func(x, y int, rows ...string) {
		for dy, row := range rows {
			for dx := 0; dx < len(row); dx++ {
				if row[dx] != '.' {
					set(x+dx, y+dy, row[dx])
				}
			}
		}
	}

	// body: an ellipse that is narrower at the top
	for y := 3; y <= 20; y++ {
		dy := (float64(y) - 12) / 9
		if dy*dy > 1 {
			continue
		}
		half := 10 * math.Sqrt(1-dy*dy) * (0.72 + 0.28*float64(y-3)/17)
		for x := 0; x < mascotW; x++ {
			if math.Abs(float64(x)-12.5) <= half {
				g[y][x] = 'g'
			}
		}
	}
	// arms are part of the silhouette, so they get the same outline as the body
	thick := func(x0, y0, x1, y1, t int) {
		steps := max(abs(x1-x0), abs(y1-y0))
		for i := 0; i <= steps; i++ {
			for dx := range t {
				for dy := range t {
					set(x0+(x1-x0)*i/max(steps, 1)+dx, y0+(y1-y0)*i/max(steps, 1)+dy, 'g')
				}
			}
		}
	}
	if e.wave {
		thick(20, 12, 23, 6, 2)
		thick(22, 3, 24, 5, 3)
	}

	// outline: any body pixel touching empty space
	outline := make([][]bool, mascotH)
	for y := range outline {
		outline[y] = make([]bool, mascotW)
		for x := 0; x < mascotW; x++ {
			if g[y][x] != 'g' {
				continue
			}
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= mascotW || ny >= mascotH || g[ny][nx] == '.' {
					outline[y][x] = true
				}
			}
		}
	}
	for y := range outline {
		for x := range outline[y] {
			if outline[y][x] {
				g[y][x] = 'k'
			}
		}
	}
	// shading: darker belly and right edge, a shine on the upper left
	for y := 3; y <= 20; y++ {
		for x := 0; x < mascotW; x++ {
			if g[y][x] != 'g' {
				continue
			}
			if y >= 18 || (x >= 19 && y >= 9) {
				g[y][x] = 'd'
			}
		}
	}
	put(6, 6, "lll", "ll.")
	put(5, 8, "l")

	// leaf sprout
	put(12, 0, "...kkk", "..kgggk", "kkkgllgk", "kggkgggk", ".kk.kkk.")
	set(12, 2, 'k')
	put(11, 3, "kk")

	// blush
	put(4, 14, "pp")
	put(20, 14, "pp")

	switch e.eyes {
	case eyesOpen:
		put(8, 10, "kk", "kk", "kk")
		put(16, 10, "kk", "kk", "kk")
		set(8, 10, 'w')
		set(16, 10, 'w')
	case eyesClosed:
		put(7, 12, "kkkk")
		put(15, 12, "kkkk")
	case eyesWorried:
		put(8, 10, "kk", "kk", "kk", "kk")
		put(16, 10, "kk", "kk", "kk", "kk")
		set(8, 10, 'w')
		set(16, 10, 'w')
		put(6, 8, "..kk", "kk..")
		put(16, 8, "kk..", "..kk")
	case eyesUp:
		put(8, 9, "kk", "kk", "kk")
		put(16, 9, "kk", "kk", "kk")
		set(8, 9, 'w')
		set(16, 9, 'w')
	}

	switch e.mouth {
	case mouthSmile:
		put(10, 14, "k....k", ".kkkk.")
	case mouthFlat:
		put(10, 15, "kkkkkk")
	case mouthO:
		put(12, 15, "kk", "kk")
	case mouthWobble:
		put(10, 15, ".kkkk.", "k....k")
	}

	if e.sweat {
		put(21, 6, ".c.", "ccc", "ccc", ".c.")
	}
	if e.cover {
		// two rounded hands over the eyes, each with finger lines
		for _, x := range []int{5, 14} {
			put(x, 7, ".kkkkkk.", "kgggggggk", "kggkggkgk", "kggkggkgk", "kgggggggk", ".kgggggk.", "..kkkkk..")
		}
	}
	if e.think {
		put(19, 0, ".kkkk.", "k....k", "....kk", "...kk.", "...k..", "......", "...k..")
	}

	rows := make([]string, mascotH)
	for y := range g {
		rows[y] = string(g[y])
	}
	return rows
}

var faviconColors = map[byte]string{
	'k': "#23331a", 'g': "#84c341", 'l': "#bfe78a", 'd': "#5d9a2b", 'w': "#ffffff", 'p': "#f4a39a", 'c': "#7fd6e6",
}

// faviconSVG renders a sprite as a standalone SVG with fixed colors, since a favicon cannot use the page's CSS.
func faviconSVG(name string) ([]byte, error) {
	rows, ok := sprites[name]
	if !ok {
		return nil, fmt.Errorf("unknown sprite %q", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, len(rows[0]), len(rows))
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			if fill, ok := faviconColors[row[x]]; ok {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`, x, y, fill)
			}
		}
	}
	b.WriteString(`</svg>`)
	return []byte(b.String()), nil
}

// spriteHTML renders a sprite as inline SVG. Runs of the same color become one rect.
func spriteHTML(name, class string) (template.HTML, error) {
	rows, ok := sprites[name]
	if !ok {
		return "", fmt.Errorf("unknown sprite %q", name)
	}
	width := 0
	for _, r := range rows {
		width = max(width, len(r))
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="px %s" viewBox="0 0 %d %d" shape-rendering="crispEdges" aria-hidden="true" focusable="false">`,
		template.HTMLEscapeString(class), width, len(rows))
	for y, row := range rows {
		for x := 0; x < len(row); {
			c := row[x]
			run := 1
			for x+run < len(row) && row[x+run] == c {
				run++
			}
			if c != '.' {
				fmt.Fprintf(&b, `<rect class="p-%c" x="%d" y="%d" width="%d" height="1"/>`, c, x, y, run)
			}
			x += run
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), nil
}
