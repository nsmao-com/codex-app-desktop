package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

const canvasSize = 1024

type point struct {
	x float64
	y float64
}

func main() {
	output := filepath.Join("build", "appicon.png")
	if len(os.Args) > 1 {
		output = os.Args[1]
	}

	canvas := image.NewRGBA(image.Rect(0, 0, canvasSize, canvasSize))
	background := color.RGBA{R: 24, G: 25, B: 21, A: 255}
	roundedRect(canvas, 58, 58, 966, 966, 218, background)

	grid := color.RGBA{R: 255, G: 255, B: 255, A: 12}
	for position := 160; position <= 864; position += 88 {
		line(canvas, point{float64(position), 98}, point{float64(position), 926}, 2, grid)
		line(canvas, point{98, float64(position)}, point{926, float64(position)}, 2, grid)
	}

	ring(canvas, 512, 512, 326, 4, color.RGBA{R: 221, G: 162, B: 80, A: 72})
	ring(canvas, 512, 512, 247, 3, color.RGBA{R: 242, G: 239, B: 230, A: 42})
	ring(canvas, 512, 512, 174, 2, color.RGBA{R: 221, G: 162, B: 80, A: 55})

	diamond(canvas, 512, 512, 246, color.RGBA{R: 221, G: 162, B: 80, A: 38})
	diamond(canvas, 512, 512, 178, color.RGBA{R: 221, G: 162, B: 80, A: 255})
	diamond(canvas, 512, 512, 105, color.RGBA{R: 24, G: 25, B: 21, A: 255})
	diamond(canvas, 512, 512, 54, color.RGBA{R: 242, G: 239, B: 230, A: 255})

	circle(canvas, 238, 290, 22, color.RGBA{R: 143, G: 180, B: 119, A: 255})
	circle(canvas, 804, 704, 14, color.RGBA{R: 242, G: 239, B: 230, A: 180})

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		log.Fatal(err)
	}
	file, err := os.Create(output)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, canvas); err != nil {
		log.Fatal(err)
	}
}

func roundedRect(canvas *image.RGBA, left, top, right, bottom, radius int, fill color.RGBA) {
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			dx := maxInt(left+radius-x, 0, x-(right-radius-1))
			dy := maxInt(top+radius-y, 0, y-(bottom-radius-1))
			if dx*dx+dy*dy <= radius*radius {
				blend(canvas, x, y, fill)
			}
		}
	}
}

func diamond(canvas *image.RGBA, centerX, centerY, radius int, fill color.RGBA) {
	polygon(canvas, []point{
		{float64(centerX), float64(centerY - radius)},
		{float64(centerX + radius), float64(centerY)},
		{float64(centerX), float64(centerY + radius)},
		{float64(centerX - radius), float64(centerY)},
	}, fill)
}

func polygon(canvas *image.RGBA, vertices []point, fill color.RGBA) {
	minX, minY := float64(canvas.Bounds().Max.X), float64(canvas.Bounds().Max.Y)
	maxX, maxY := 0.0, 0.0
	for _, vertex := range vertices {
		minX = math.Min(minX, vertex.x)
		minY = math.Min(minY, vertex.y)
		maxX = math.Max(maxX, vertex.x)
		maxY = math.Max(maxY, vertex.y)
	}
	for y := int(minY); y <= int(maxY); y++ {
		for x := int(minX); x <= int(maxX); x++ {
			inside := false
			previous := len(vertices) - 1
			for current := range vertices {
				a, b := vertices[current], vertices[previous]
				intersects := (a.y > float64(y)) != (b.y > float64(y)) &&
					float64(x) < (b.x-a.x)*(float64(y)-a.y)/(b.y-a.y)+a.x
				if intersects {
					inside = !inside
				}
				previous = current
			}
			if inside {
				blend(canvas, x, y, fill)
			}
		}
	}
}

func ring(canvas *image.RGBA, centerX, centerY, radius, width int, fill color.RGBA) {
	inner := float64(radius - width)
	outer := float64(radius + width)
	for y := centerY - radius - width; y <= centerY+radius+width; y++ {
		for x := centerX - radius - width; x <= centerX+radius+width; x++ {
			distance := math.Hypot(float64(x-centerX), float64(y-centerY))
			if distance >= inner && distance <= outer {
				blend(canvas, x, y, fill)
			}
		}
	}
}

func circle(canvas *image.RGBA, centerX, centerY, radius int, fill color.RGBA) {
	for y := centerY - radius; y <= centerY+radius; y++ {
		for x := centerX - radius; x <= centerX+radius; x++ {
			if (x-centerX)*(x-centerX)+(y-centerY)*(y-centerY) <= radius*radius {
				blend(canvas, x, y, fill)
			}
		}
	}
}

func line(canvas *image.RGBA, start, end point, width int, fill color.RGBA) {
	distance := math.Hypot(end.x-start.x, end.y-start.y)
	for step := 0; step <= int(distance); step++ {
		ratio := float64(step) / distance
		x := int(start.x + (end.x-start.x)*ratio)
		y := int(start.y + (end.y-start.y)*ratio)
		circle(canvas, x, y, width, fill)
	}
}

func blend(canvas *image.RGBA, x, y int, source color.RGBA) {
	if !image.Pt(x, y).In(canvas.Bounds()) {
		return
	}
	destination := canvas.RGBAAt(x, y)
	alpha := float64(source.A) / 255
	canvas.SetRGBA(x, y, color.RGBA{
		R: uint8(float64(source.R)*alpha + float64(destination.R)*(1-alpha)),
		G: uint8(float64(source.G)*alpha + float64(destination.G)*(1-alpha)),
		B: uint8(float64(source.B)*alpha + float64(destination.B)*(1-alpha)),
		A: uint8(float64(source.A) + float64(destination.A)*(1-alpha)),
	})
}

func maxInt(values ...int) int {
	result := values[0]
	for _, value := range values[1:] {
		if value > result {
			result = value
		}
	}
	return result
}
