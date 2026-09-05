package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

const canvasSize = 1024

// One mark for the desktop, macOS Icon Composer and in-app branding. Keep the
// vector geometry identical to the raster drawing below (no fonts required).
const markSVG = `<path d="M322 688V352L688 688V352" fill="none" stroke="#f6f0e5" stroke-width="144" stroke-linecap="round" stroke-linejoin="round"/>
  <circle cx="788" cy="230" r="44" fill="#eea56b"/>`

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
	background := color.RGBA{R: 23, G: 59, B: 54, A: 255}
	roundedRect(canvas, 64, 64, 960, 960, 224, background)
	letter := color.RGBA{R: 246, G: 240, B: 229, A: 255}
	line(canvas, point{322, 688}, point{322, 352}, 72, letter)
	line(canvas, point{322, 352}, point{688, 688}, 72, letter)
	line(canvas, point{688, 688}, point{688, 352}, 72, letter)
	circle(canvas, 788, 230, 44, color.RGBA{R: 238, G: 165, B: 107, A: 255})

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
	// Derive companion assets from the PNG output location, so the generator
	// works both from the repository root and the build Taskfile directory.
	buildDir := filepath.Dir(output)
	writeSVG(filepath.Join(buildDir, "appicon.icon", "Assets", "nice_codex_vector.svg"), markSVG)
	writeSVG(filepath.Join(buildDir, "..", "frontend", "public", "nice-mark.svg"),
		`<rect x="64" y="64" width="896" height="896" rx="224" fill="#173b36"/>`+"\n  "+markSVG)
}

func writeSVG(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	svg := fmt.Sprintf("<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 1024 1024\">\n  %s\n</svg>\n", content)
	if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
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
