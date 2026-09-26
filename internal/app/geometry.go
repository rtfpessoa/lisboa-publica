package app

import (
	"math"
	"sort"
)

// simplifyShape keeps official vertices and endpoints at a two-metre local projection tolerance.
func simplifyShape(points []shapePoint) [][]float64 {
	sort.Slice(points, func(i, j int) bool { return points[i].Sequence < points[j].Sequence })
	keep := shapeVertexMask(points)
	count := 0
	for _, retained := range keep {
		if retained {
			count++
		}
	}
	coordinates := make([][]float64, 0, count)
	for i, p := range points {
		if keep[i] {
			coordinates = append(coordinates, []float64{p.Lon, p.Lat})
		}
	}
	return coordinates
}

func segmentDistanceSquared(p, a, b shapePoint, scale float64) float64 {
	x, y := (p.Lon-a.Lon)*scale, (p.Lat-a.Lat)*metresPerDegree
	dx, dy := (b.Lon-a.Lon)*scale, (b.Lat-a.Lat)*metresPerDegree
	t := 0.0
	if length := dx*dx + dy*dy; length > 0 {
		t = math.Max(0, math.Min(1, (x*dx+y*dy)/length))
	}
	x, y = x-t*dx, y-t*dy
	return x*x + y*y
}

func greatestShapeDeviation(points []shapePoint, first, last int, scale float64) int {
	maximum, index := geometryToleranceMetres*geometryToleranceMetres, -1
	for i := first + 1; i < last; i++ {
		distance := segmentDistanceSquared(points[i], points[first], points[last], scale)
		if distance > maximum {
			maximum, index = distance, i
		}
	}
	return index
}

func shapeVertexMask(points []shapePoint) []bool {
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	scale := metresPerDegree * math.Cos(points[0].Lat*math.Pi/180)
	stack := [][2]int{{0, len(points) - 1}}
	for len(stack) > 0 {
		segment := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		first, last := segment[0], segment[1]
		index := greatestShapeDeviation(points, first, last, scale)
		if index >= 0 {
			keep[index] = true
			stack = append(stack, [2]int{first, index}, [2]int{index, last})
		}
	}
	return keep
}
