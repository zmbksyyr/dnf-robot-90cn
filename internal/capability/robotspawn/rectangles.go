package robotspawn

import (
	"math"
	"sort"

	"robot/internal/shared"
)

type RangeRandom interface {
	RandBetween(min, max int) int
}

func MapRectangles(mp shared.MapCatalogItem) []shared.MapRectangle {
	if len(mp.Rectangles) > 0 {
		out := make([]shared.MapRectangle, 0, len(mp.Rectangles))
		for _, rectangle := range mp.Rectangles {
			if ValidRectangle(rectangle) {
				out = append(out, rectangle)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	fallback := shared.MapRectangle{XMin: mp.XMin, XMax: mp.XMax, YMin: mp.YMin, YMax: mp.YMax}
	if !ValidRectangle(fallback) {
		return nil
	}
	return []shared.MapRectangle{fallback}
}

func ValidRectangle(rectangle shared.MapRectangle) bool {
	return rectangle.XMax >= rectangle.XMin && rectangle.YMax >= rectangle.YMin
}

func RectangleContains(rectangle shared.MapRectangle, x, y int) bool {
	return x >= rectangle.XMin && x <= rectangle.XMax && y >= rectangle.YMin && y <= rectangle.YMax
}

func RectangleArea(rectangle shared.MapRectangle) int {
	width := rectangle.XMax - rectangle.XMin + 1
	height := rectangle.YMax - rectangle.YMin + 1
	if width <= 0 || height <= 0 {
		return 0
	}
	return width * height
}

func SmoothedRectanglesWeight(rectangles []shared.MapRectangle) int {
	area := 0
	for _, rectangle := range NormalizeRectangles(rectangles) {
		area += RectangleArea(rectangle)
	}
	return smoothedAreaWeight(area)
}

// NormalizeRectangles returns a non-overlapping representation of the same
// integer-coordinate area. PVF movement geometry commonly contains overlaps.
func NormalizeRectangles(rectangles []shared.MapRectangle) []shared.MapRectangle {
	valid := make([]shared.MapRectangle, 0, len(rectangles))
	xEdges := make([]int, 0, len(rectangles)*2)
	for _, rectangle := range rectangles {
		if !ValidRectangle(rectangle) {
			continue
		}
		valid = append(valid, rectangle)
		xEdges = append(xEdges, rectangle.XMin, rectangle.XMax+1)
	}
	if len(valid) == 0 {
		return nil
	}
	sort.Ints(xEdges)
	uniqueX := xEdges[:0]
	for _, x := range xEdges {
		if len(uniqueX) == 0 || uniqueX[len(uniqueX)-1] != x {
			uniqueX = append(uniqueX, x)
		}
	}

	out := make([]shared.MapRectangle, 0, len(valid))
	for index := 0; index+1 < len(uniqueX); index++ {
		xMin, xMax := uniqueX[index], uniqueX[index+1]-1
		if xMin > xMax {
			continue
		}
		yRanges := make([]shared.MapRectangle, 0, len(valid))
		for _, rectangle := range valid {
			if rectangle.XMin <= xMin && rectangle.XMax >= xMax {
				yRanges = append(yRanges, rectangle)
			}
		}
		sort.Slice(yRanges, func(i, j int) bool {
			if yRanges[i].YMin == yRanges[j].YMin {
				return yRanges[i].YMax < yRanges[j].YMax
			}
			return yRanges[i].YMin < yRanges[j].YMin
		})
		for _, rectangle := range yRanges {
			if len(out) > 0 {
				last := &out[len(out)-1]
				if last.XMin == xMin && last.XMax == xMax && rectangle.YMin <= last.YMax+1 {
					if rectangle.YMax > last.YMax {
						last.YMax = rectangle.YMax
					}
					continue
				}
			}
			out = append(out, shared.MapRectangle{XMin: xMin, XMax: xMax, YMin: rectangle.YMin, YMax: rectangle.YMax})
		}
	}
	return out
}

func smoothedAreaWeight(area int) int {
	if area <= 0 {
		return 1
	}
	weight := int(math.Sqrt(float64(area)))
	if weight < 1 {
		return 1
	}
	return weight
}

func IntersectRectangles(rectangles []shared.MapRectangle, bounds shared.MapRectangle) []shared.MapRectangle {
	if !ValidRectangle(bounds) {
		return nil
	}
	out := make([]shared.MapRectangle, 0, len(rectangles))
	for _, rectangle := range rectangles {
		intersection := shared.MapRectangle{
			XMin: maxInt(rectangle.XMin, bounds.XMin),
			XMax: minInt(rectangle.XMax, bounds.XMax),
			YMin: maxInt(rectangle.YMin, bounds.YMin),
			YMax: minInt(rectangle.YMax, bounds.YMax),
		}
		if ValidRectangle(intersection) {
			out = append(out, intersection)
		}
	}
	return out
}

func RandomPoint(env RangeRandom, rectangles []shared.MapRectangle) (x, y int, ok bool) {
	return randomPointFromNormalized(env, NormalizeRectangles(rectangles))
}

func randomPointFromNormalized(env RangeRandom, rectangles []shared.MapRectangle) (x, y int, ok bool) {
	if env == nil || len(rectangles) == 0 {
		return 0, 0, false
	}
	totalWeight := 0
	for _, rectangle := range rectangles {
		totalWeight += RectangleArea(rectangle)
	}
	choice := env.RandBetween(1, totalWeight)
	if choice < 1 || choice > totalWeight {
		choice = 1
	}
	selected := rectangles[0]
	for _, rectangle := range rectangles {
		choice -= RectangleArea(rectangle)
		if choice <= 0 {
			selected = rectangle
			break
		}
	}
	return env.RandBetween(selected.XMin, selected.XMax), env.RandBetween(selected.YMin, selected.YMax), true
}

func RandomPointInMap(env RangeRandom, mp shared.MapCatalogItem) (x, y int, ok bool) {
	return RandomPoint(env, MapRectangles(mp))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
