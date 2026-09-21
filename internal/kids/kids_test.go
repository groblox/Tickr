package kids

import (
	"strings"
	"testing"
)

func TestMazeIsSolvableAndPerfect(t *testing.T) {
	for seed := int64(1); seed <= 25; seed++ {
		m := NewMaze(6, 8, seed)
		path := m.Solve()
		if len(path) == 0 {
			t.Fatalf("seed %d: maze has no solution", seed)
		}
		if path[0] != [2]int{0, 0} || path[len(path)-1] != [2]int{5, 7} {
			t.Fatalf("seed %d: path endpoints wrong: %v", seed, path)
		}
		// A perfect maze on w*h cells has exactly w*h-1 removed internal walls,
		// so counting standing internal walls checks there are no loops.
		standing := 0
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				if x+1 < m.W && m.Walls[y][x]&WallE != 0 {
					standing++
				}
				if y+1 < m.H && m.Walls[y][x]&WallS != 0 {
					standing++
				}
			}
		}
		internal := (m.W-1)*m.H + (m.H-1)*m.W
		if standing != internal-(m.W*m.H-1) {
			t.Fatalf("seed %d: maze is not a perfect tree (standing=%d)", seed, standing)
		}
	}
}

func TestMazeDeterministic(t *testing.T) {
	a := NewMaze(5, 5, 42).SVG(120)
	b := NewMaze(5, 5, 42).SVG(120)
	if a != b {
		t.Fatal("same seed produced different mazes")
	}
	if !strings.HasPrefix(a, "<svg") || !strings.HasSuffix(a, "</svg>") {
		t.Fatal("svg output malformed")
	}
}

func TestShapesRender(t *testing.T) {
	for _, s := range Shapes {
		svg := ShapeSVG(s, 80, true)
		if !strings.Contains(svg, "stroke-dasharray") || !strings.Contains(svg, "</svg>") {
			t.Fatalf("shape %s rendered badly: %s", s.Name, svg)
		}
	}
	if !strings.Contains(TraceLetterSVG("B", 60), ">B<") {
		t.Fatal("trace letter missing glyph")
	}
	if strings.Count(CountingRowSVG(7, 5, 20), "<path") != 7 {
		t.Fatal("counting row should draw 7 stars")
	}
}
