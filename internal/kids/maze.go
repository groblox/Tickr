// Package kids holds generators for printable activities aimed at toddlers.
package kids

import (
	"fmt"
	"math/rand"
	"strings"
)

// Maze is a rectangular grid maze. Walls[y][x] holds a bitmask of the four
// walls still standing around cell (x, y).
type Maze struct {
	W, H  int
	Walls [][]uint8
}

// Wall bits.
const (
	WallN uint8 = 1 << iota
	WallE
	WallS
	WallW
)

// NewMaze carves a perfect maze with a recursive backtracker. The seed makes
// the maze reproducible for a given day.
func NewMaze(w, h int, seed int64) *Maze {
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	m := &Maze{W: w, H: h, Walls: make([][]uint8, h)}
	for y := range m.Walls {
		m.Walls[y] = make([]uint8, w)
		for x := range m.Walls[y] {
			m.Walls[y][x] = WallN | WallE | WallS | WallW
		}
	}
	rng := rand.New(rand.NewSource(seed))
	visited := make([][]bool, h)
	for y := range visited {
		visited[y] = make([]bool, w)
	}
	type pt struct{ x, y int }
	stack := []pt{{0, 0}}
	visited[0][0] = true
	dirs := []struct {
		dx, dy   int
		wall, op uint8
	}{{0, -1, WallN, WallS}, {1, 0, WallE, WallW}, {0, 1, WallS, WallN}, {-1, 0, WallW, WallE}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		var options []int
		for i, d := range dirs {
			nx, ny := cur.x+d.dx, cur.y+d.dy
			if nx >= 0 && ny >= 0 && nx < w && ny < h && !visited[ny][nx] {
				options = append(options, i)
			}
		}
		if len(options) == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		d := dirs[options[rng.Intn(len(options))]]
		nx, ny := cur.x+d.dx, cur.y+d.dy
		m.Walls[cur.y][cur.x] &^= d.wall
		m.Walls[ny][nx] &^= d.op
		visited[ny][nx] = true
		stack = append(stack, pt{nx, ny})
	}
	// Open an entrance at the top-left and an exit at the bottom-right.
	m.Walls[0][0] &^= WallN
	m.Walls[h-1][w-1] &^= WallS
	return m
}

// SVG renders the maze at the given pixel width with thick, toddler-friendly walls.
func (m *Maze) SVG(widthPx int) string {
	cell := float64(widthPx) / float64(m.W)
	height := cell * float64(m.H)
	stroke := cell / 6
	if stroke < 2 {
		stroke = 2
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%.0f" viewBox="0 0 %d %.0f">`, widthPx, height, widthPx, height)
	fmt.Fprintf(&b, `<g stroke="#000" stroke-width="%.1f" stroke-linecap="round" fill="none">`, stroke)
	line := func(x1, y1, x2, y2 float64) {
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x1, y1, x2, y2)
	}
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			wl := m.Walls[y][x]
			x0, y0 := float64(x)*cell, float64(y)*cell
			x1, y1 := x0+cell, y0+cell
			if wl&WallN != 0 {
				line(x0, y0, x1, y0)
			}
			if wl&WallW != 0 {
				line(x0, y0, x0, y1)
			}
			if wl&WallS != 0 {
				line(x0, y1, x1, y1)
			}
			if wl&WallE != 0 {
				line(x1, y0, x1, y1)
			}
		}
	}
	b.WriteString(`</g>`)
	// Start star and finish flag markers.
	sx, sy := cell/2, cell/2
	ex, ey := float64(m.W)*cell-cell/2, float64(m.H)*cell-cell/2
	r := cell / 5
	fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="#000"/>`, sx, sy, r)
	fmt.Fprintf(&b, `<path d="%s" fill="#000"/>`, starPath(ex, ey, r*1.3))
	b.WriteString(`</svg>`)
	return b.String()
}

// Solve returns the path from entrance to exit as (x, y) cells, for tests.
func (m *Maze) Solve() [][2]int {
	type node struct{ x, y int }
	prev := map[node]node{}
	start := node{0, 0}
	goal := node{m.W - 1, m.H - 1}
	queue := []node{start}
	seen := map[node]bool{start: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == goal {
			break
		}
		wl := m.Walls[cur.y][cur.x]
		try := func(nx, ny int, blocked bool) {
			if blocked || nx < 0 || ny < 0 || nx >= m.W || ny >= m.H {
				return
			}
			n := node{nx, ny}
			if !seen[n] {
				seen[n] = true
				prev[n] = cur
				queue = append(queue, n)
			}
		}
		try(cur.x, cur.y-1, wl&WallN != 0)
		try(cur.x+1, cur.y, wl&WallE != 0)
		try(cur.x, cur.y+1, wl&WallS != 0)
		try(cur.x-1, cur.y, wl&WallW != 0)
	}
	if !seen[goal] {
		return nil
	}
	var path [][2]int
	for n := goal; ; n = prev[n] {
		path = append([][2]int{{n.x, n.y}}, path...)
		if n == start {
			break
		}
	}
	return path
}
