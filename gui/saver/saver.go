package saver

import (
	"image"
	"image/color"
	"image/draw"
	"math/rand"
	"time"

	"seedhammer.com/gui/assets"
	"seedhammer.com/rgb16"
)

type State struct {
	snake []joint
	food  struct {
		color int
		image.Point
	}
	dx, dy int
	shY    float32
	shV    float32
	sY     float32
	sV     float32
	mode   mode
	delay  int

	clear struct {
		x int
		y int
	}
}

type mode int

const (
	modeClear mode = iota
	modeSnake
	modeGameOver
)

type joint struct {
	filled bool
	image.Point
}

const gridSize = 8
const snakeLen = 5

var colors = []color.RGBA64{
	rgb64(0xff0000), // Red
	rgb64(0xffa202), // Orange Peel
	rgb64(0xffff00), // Yellow
	rgb64(0x00ff00), // Green
	rgb64(0x00fff2), // Cyan / Aqua
	rgb64(0x0097fe), // Azure Radiance
	rgb64(0xe000ff), // Electric Violet
	rgb64(0xff00aa), // Hollywood Cerise
}

func resetScreenSaver(s *State, width int, height int) {
	s.delay = 20
	s.shY = 0
	s.shV = -20
	s.sY = 0
	s.sV = 0
	s.mode = modeSnake
	rand.Seed(time.Now().UnixNano())
	location := image.Point{
		X: rand.Intn(width / gridSize),
		Y: rand.Intn(height / gridSize),
	}
	switch rand.Intn(4) {
	case 0:
		s.dx = 0
		s.dy = -1
		location.Y = height + snakeLen
	case 1:
		s.dx = 1
		s.dy = 0
		location.X = -snakeLen - 1
	case 2:
		s.dx = 0
		s.dy = 1
		location.Y = -snakeLen - 1
	case 3:
		s.dx = -1
		s.dy = 0
		location.X = width + snakeLen
	}

	s.snake = []joint{}
	for len(s.snake) < snakeLen {
		location.X += s.dx
		location.Y += s.dy
		s.snake = append(s.snake, joint{Point: location})

	}
	placeFood(s, width, height)
}

func placeFood(s *State, width int, height int) {
outer:
	for {
		s.food.X = rand.Intn(width/gridSize-2*1) + 1
		s.food.Y = rand.Intn(height/gridSize-2*1) + 1
		for _, j := range s.snake {
			if j.Point == s.food.Point {
				continue outer
			}

		}
		break
	}
}

func clearScreenSaver(s *State, screen *rgb16.Image) {
	counter := 0
	for counter < 3 {
		width, height := screen.Bounds().Dx(), screen.Bounds().Dy()
		clearBox(screen, s.clear.x*gridSize, s.clear.y*gridSize, rgb64(0x000000))
		s.snake = append(s.snake, joint{Point: image.Point{
			s.clear.x,
			s.clear.y,
		}})
		if len(s.snake) > snakeLen {
			tail := s.snake[0]
			if tail.Y*gridSize == height {
				s.mode = modeSnake
				resetScreenSaver(s, width, height)
				return
			}
			clearBox(screen, tail.X*gridSize, tail.Y*gridSize, rgb64(0x000000))
			s.snake = append(s.snake[:0], s.snake[1:]...)
		}
		if s.clear.y%2 == 0 {
			s.clear.x += 1
		} else {
			s.clear.x -= 1
		}
		if s.clear.x*gridSize >= width || s.clear.x*gridSize < 0 {
			s.clear.y += 1
		}
		counter += 1
	}
	drawSnake(screen, s)
}

func Draw(s *State, screen *rgb16.Image) {
	if s.delay > 0 {
		s.delay -= 1
		return
	}
	if s.mode == modeClear {
		clearScreenSaver(s, screen)
		return
	}

	screen.Clear(color.Black)

	width, height := screen.Bounds().Dx(), screen.Bounds().Dy()
	head := s.snake[len(s.snake)-1]
	switch {
	case s.food.X < head.X:
		s.dx = -1
	case s.food.X > head.X:
		s.dx = 1
	case s.food.X == head.X:
		s.dx = 0
	}
	switch {
	case s.food.Y < head.Y:
		s.dy = -1
	case s.food.Y > head.Y:
		s.dy = 1
	case s.food.Y == head.Y:
		s.dy = 0
	}
	if s.dx != 0 {
		s.dy = 0
	}

update:
	for s.mode == modeSnake {
		newHead := image.Point{X: head.X + s.dx, Y: head.Y + s.dy}
		if newHead.X < 0 {
			newHead.X = width/gridSize - 1
		} else if newHead.X > width/gridSize-1 {
			newHead.X = 0
		}
		if newHead.Y < 0 {
			newHead.Y = height/gridSize - 1
		} else if newHead.Y > height/gridSize-1 {
			newHead.Y = 0
		}
		neck := s.snake[len(s.snake)-2]

		if neck.Point == newHead {
			s.dx *= -1
			s.dy *= -1
			continue
		}
		for _, j := range s.snake {
			if j.Point == newHead {
				s.mode = modeGameOver
				continue update
			}

		}
		j := joint{
			Point: newHead,
		}
		if newHead == s.food.Point {
			s.snake = append(s.snake, j)
			placeFood(s, width, height)
			for {
				color := rand.Intn(len(colors))
				if color != s.food.color {
					s.food.color = color
					break
				}
			}
		} else {
			s.snake = append(s.snake[:0], s.snake[1:]...)
			s.snake = append(s.snake, j)
		}
		break
	}
	for s.mode == modeGameOver {
		minY := 1000
		for _, c := range s.snake {
			if c.Y < minY {
				minY = c.Y
			}
		}
		const a = 1.7
		s.shV += a
		s.shY += s.shV
		const b = -3.5
		s.sV += b
		if s.sV < 0 {
			s.sV = 0
		}
		s.sY += s.sV
		sTop := minY*gridSize + int(s.sY)
		if sTop < int(s.shY) && sTop < height {
			s.shY = float32(minY)*gridSize + s.sY
			s.sV = s.shV
			s.shV = -s.shV * 0.8
		}
		shTop := int(s.shY) - 11*gridSize
		if shTop > height {
			resetScreenSaver(s, width, height)
			break
		}

		drawSeedHammer(screen, shTop)
		break
	}
	drawSnake(screen, s)
	if s.mode == modeSnake {
		draw.DrawMask(
			screen,
			assets.LogoSmall.Bounds().Add(image.Pt(s.food.X*gridSize-6, s.food.Y*gridSize-3)),
			image.NewUniform(colors[s.food.color]),
			image.Pt(0, 0),
			assets.LogoSmall,
			image.Pt(0, 0),
			draw.Over,
		)
	}

}

func drawSnake(screen draw.RGBA64Image, s *State) {
	for i, j := range s.snake {
		color := rgb64(0xd9d9d9)
		if i == len(s.snake)-1 {
			color = rgb64(0xffffff)
		}
		if j.filled {
			clearBox(screen, j.X*gridSize, j.Y*gridSize+int(s.sY), color)
		} else {
			drawBox(screen, j.X*gridSize, j.Y*gridSize+int(s.sY), color)
		}
	}
}

func drawSeedHammer(screen draw.RGBA64Image, y int) {
	S := []image.Point{
		{0, 0}, {1, 0}, {2, 0},
		{0, 1},
		{0, 2}, {1, 2}, {2, 2},
		{2, 3},
		{0, 4}, {1, 4}, {2, 4},
	}
	E := []image.Point{
		{0, 0}, {1, 0}, {2, 0},
		{0, 1},
		{0, 2}, {1, 2}, {2, 2},
		{0, 3},
		{0, 4}, {1, 4}, {2, 4},
	}

	D := []image.Point{
		{0, 0}, {1, 0}, {2, 0},
		{0, 1}, {3, 1},
		{0, 2}, {3, 2},
		{0, 3}, {3, 3},
		{0, 4}, {1, 4}, {2, 4},
	}
	A := []image.Point{
		{1, 0}, {2, 0},
		{0, 1}, {3, 1},
		{0, 2}, {1, 2}, {2, 2}, {3, 2},
		{0, 3}, {3, 3},
		{0, 4}, {3, 4},
	}
	H := []image.Point{
		{0, 0}, {3, 0},
		{0, 1}, {3, 1},
		{0, 2}, {1, 2}, {2, 2}, {3, 2},
		{0, 3}, {3, 3},
		{0, 4}, {3, 4},
	}
	M := []image.Point{
		{0, 0}, {4, 0},
		{0, 1}, {1, 1}, {3, 1}, {4, 1},
		{0, 2}, {1, 2}, {2, 2}, {3, 2}, {4, 2},
		{0, 3}, {2, 3}, {4, 3},
		{0, 4}, {4, 4},
	}

	R := []image.Point{
		{0, 0}, {1, 0}, {2, 0},
		{0, 1}, {3, 1},
		{0, 2}, {1, 2}, {2, 2},
		{0, 3}, {3, 3},
		{0, 4}, {3, 4},
	}

	drawBoxes(screen, S, (0+7)*gridSize, 0*gridSize+y)
	drawBoxes(screen, E, (4+7)*gridSize, 0*gridSize+y)
	drawBoxes(screen, E, (8+7)*gridSize, 0*gridSize+y)
	drawBoxes(screen, D, (12+7)*gridSize, 0*gridSize+y)
	drawBoxes(screen, H, 0*gridSize, 6*gridSize+y)
	drawBoxes(screen, A, 5*gridSize, 6*gridSize+y)
	drawBoxes(screen, M, 10*gridSize, 6*gridSize+y)
	drawBoxes(screen, M, 16*gridSize, 6*gridSize+y)
	drawBoxes(screen, E, 22*gridSize, 6*gridSize+y)
	drawBoxes(screen, R, 26*gridSize, 6*gridSize+y)

}

func drawBoxes(screen draw.RGBA64Image, boxes []image.Point, x, y int) {
	for _, c := range boxes {
		drawBox(screen, c.X*gridSize+x, c.Y*gridSize+y, rgb64(0xffffff))
	}
}

func clearBox(screen draw.RGBA64Image, x, y int, color color.RGBA64) {
	const boxSize = gridSize
	x1 := x

	y1 := y
	for x < x1+boxSize {

		y = y1
		for y < y1+boxSize {
			screen.SetRGBA64(x, y, color)
			y += 1
		}
		x += 1
	}
}

func drawBox(screen draw.RGBA64Image, x, y int, color color.RGBA64) {
	const boxSize = gridSize - 2
	x1 := x

	y1 := y
	for x < x1+boxSize {

		y = y1
		for y < y1+boxSize {
			screen.SetRGBA64(x+1, y+1, color)
			y += 1
		}
		x += 1
	}
}

func rgb64(c uint32) color.RGBA64 {
	r := uint16(c>>16) & 0xff
	g := uint16(c>>8) & 0xff
	b := uint16(c) & 0xff
	return color.RGBA64{
		A: 0xffff,
		R: r<<8 | r,
		G: g<<8 | g,
		B: b<<8 | b,
	}
}
