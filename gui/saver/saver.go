package saver

import (
	"image"
	"image/color"
	"image/draw"
	"math/rand/v2"
	"strings"

	"golang.org/x/image/math/fixed"
	"seedhammer.com/gui/assets"
	"seedhammer.com/image/rgb565"
)

type State struct {
	prev struct {
		snake image.Rectangle
		logo  image.Rectangle
	}
	snake    []joint
	snakeBuf [snakeLen + 20]joint
	food     struct {
		color int
		image.Point
	}
	dx, dy int
	shY    fixed.Int26_6
	shV    fixed.Int26_6
	sY     fixed.Int26_6
	sV     fixed.Int26_6
	shTop  int
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

type logo struct {
	Bounds image.Rectangle
	boxes  string
}

var (
	logoNarrow = buildLogo(false)
	logoWide   = buildLogo(true)
)

var (
	tail  = rgb(0xd9d9d9)
	white = rgb(0xffffff)
	black = rgb(0x000000)
)

var colors = []image.Image{
	rgb(0xff0000), // Red
	rgb(0xffa202), // Orange Peel
	rgb(0xffff00), // Yellow
	rgb(0x00ff00), // Green
	rgb(0x00fff2), // Cyan / Aqua
	rgb(0x0097fe), // Azure Radiance
	rgb(0xe000ff), // Electric Violet
	rgb(0xff00aa), // Hollywood Cerise
}

func logoFor(width int) logo {
	if width > 400 {
		return logoWide
	} else {
		return logoNarrow
	}
}

func (s *State) reset(dims image.Point) {
	s.delay = 20
	s.shY = 0
	s.shV = fixed.I(-20)
	s.sY = 0
	s.sV = 0
	s.mode = modeSnake
	location := image.Point{
		X: rand.IntN(dims.X / gridSize),
		Y: rand.IntN(dims.Y / gridSize),
	}
	switch rand.IntN(4) {
	case 0:
		s.dx = 0
		s.dy = -1
		location.Y = dims.Y + snakeLen
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
		location.X = dims.X + snakeLen
	}

	s.snake = s.snakeBuf[:0]
	for len(s.snake) < snakeLen {
		location.X += s.dx
		location.Y += s.dy
		s.snake = append(s.snake, joint{Point: location})
	}
	placeFood(s, dims)
}

func placeFood(s *State, dims image.Point) {
outer:
	for {
		s.food.X = rand.IntN(dims.X/gridSize-2*1) + 1
		s.food.Y = rand.IntN(dims.Y/gridSize-2*1) + 1
		for _, j := range s.snake {
			if j.Point == s.food.Point {
				continue outer
			}
		}
		break
	}
}

func (s *State) stepClear(dims image.Point) {
	for i := 0; i < 3; i++ {
		s.snake = append(s.snake, joint{Point: image.Point{
			s.clear.x,
			s.clear.y,
		}})
		if len(s.snake) > snakeLen {
			tail := s.snake[0]
			if tail.Y*gridSize == dims.Y {
				s.mode = modeSnake
				s.reset(dims)
				return
			}
			s.snake = append(s.snake[:0], s.snake[1:]...)
		}
		if s.clear.y%2 == 0 {
			s.clear.x += 1
		} else {
			s.clear.x -= 1
		}
		if s.clear.x*gridSize >= dims.X || s.clear.x*gridSize < 0 {
			s.clear.y += 1
		}
	}
}

func (s *State) update(dims image.Point) {
	if s.delay > 0 {
		s.delay -= 1
		return
	}
	if s.mode == modeClear {
		s.stepClear(dims)
		return
	}

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
			newHead.X = dims.X/gridSize - 1
		} else if newHead.X > dims.X/gridSize-1 {
			newHead.X = 0
		}
		if newHead.Y < 0 {
			newHead.Y = dims.Y/gridSize - 1
		} else if newHead.Y > dims.Y/gridSize-1 {
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
		grow := newHead == s.food.Point
		if grow {
			placeFood(s, dims)
			for {
				color := rand.IntN(len(colors))
				if color != s.food.color {
					s.food.color = color
					break
				}
			}
		}
		if !grow || len(s.snake) == cap(s.snake) {
			s.snake = append(s.snake[:0], s.snake[1:]...)
		}
		s.snake = append(s.snake, j)
		break
	}
	if s.mode == modeGameOver {
		minY := 1000
		for _, c := range s.snake {
			if c.Y < minY {
				minY = c.Y
			}
		}
		const a = fixed.Int26_6(1.7*10*64) / 10
		const b = fixed.Int26_6(-3.5*10*64) / 10
		s.shV += a
		s.shY += s.shV
		s.sV += b
		if s.sV < 0 {
			s.sV = 0
		}
		s.sY += s.sV
		sTop := fixed.I(minY*gridSize) + s.sY
		if sTop < s.shY && sTop < fixed.I(dims.Y) {
			s.shY = fixed.I(minY*gridSize) + s.sY
			s.sV = s.shV
			const k = fixed.Int26_6(0.8*10*64) / 10
			s.shV = -s.shV.Mul(k)
		}
		l := logoFor(dims.X)
		s.shTop = s.shY.Round() - l.Bounds.Dy()
		if s.shTop > dims.Y {
			s.reset(dims)
		}
	}
}

type Screen interface {
	DisplaySize() image.Point
	// Dirty begins a refresh of the content
	// specified by r.
	Dirty(r image.Rectangle) error
	// NextChunk returns the next chunk of the refresh.
	NextChunk() (draw.RGBA64Image, bool)
}

func imageDraw(dst draw.RGBA64Image, dr image.Rectangle, src image.Image, sp image.Point, op draw.Op) {
	switch dst := dst.(type) {
	case *rgb565.Image:
		dst.Draw(dr, src, sp, op)
		return
	}
	draw.Draw(dst, dr, src, sp, op)
}

type chunks struct {
	scr Screen
}

func (c chunks) Next() (draw.RGBA64Image, bool) {
	img, ok := c.scr.NextChunk()
	if !ok {
		return nil, false
	}
	imageDraw(img, img.Bounds(), black, image.Point{}, draw.Src)
	return img, true
}

func newDraw(screen Screen, dr image.Rectangle) chunks {
	screen.Dirty(dr)
	return chunks{screen}
}

func (s *State) Draw(screen Screen) {
	dims := screen.DisplaySize()
	s.update(dims)
	lr := s.prev.logo
	s.prev.logo = image.Rectangle{}
	var logo logo
	if s.mode == modeGameOver {
		logo = logoFor(dims.X)
		centerx := (dims.X - logo.Bounds.Dx()) / 2
		s.prev.logo = logo.Bounds.Add(image.Pt(centerx, s.shTop))
		lr = lr.Union(s.prev.logo)
	}
	chunks := newDraw(screen, lr)
	b := s.prev.logo
	off := b.Min
	for {
		chunk, ok := chunks.Next()
		if !ok {
			break
		}
		if s.mode != modeGameOver {
			continue
		}
		for i := 0; i < len(logo.boxes); i += 2 {
			x, y := int(logo.boxes[i]), int(logo.boxes[i+1])
			drawBox(chunk, x*gridSize+off.X, y*gridSize+off.Y, white)
		}
	}
	var snake image.Rectangle
	for _, j := range s.snake {
		m := image.Pt(j.X*gridSize, j.Y*gridSize+s.sY.Round())
		snake = snake.Union(image.Rectangle{
			Min: m,
			Max: m.Add(image.Pt(boxSize, boxSize)),
		})
	}
	food := assets.LogoSmall.Bounds().Add(image.Pt(s.food.X*gridSize-6, s.food.Y*gridSize-3))
	if s.mode == modeSnake {
		snake = snake.Union(food)
	}
	chunks = newDraw(screen, snake.Union(s.prev.snake))
	for {
		chunk, ok := chunks.Next()
		if !ok {
			break
		}
		s.drawSnake(chunk)
		if s.mode != modeSnake {
			continue
		}
		draw.DrawMask(
			chunk,
			food,
			colors[s.food.color],
			image.Pt(0, 0),
			assets.LogoSmall,
			image.Pt(0, 0),
			draw.Over,
		)
	}
	s.prev.snake = snake
}

func (s *State) drawSnake(screen draw.RGBA64Image) {
	for i, j := range s.snake {
		color := tail
		if i == len(s.snake)-1 {
			color = white
		}
		p := image.Pt(j.X*gridSize, j.Y*gridSize+s.sY.Round())
		if j.filled {
			clearBox(screen, p.X, p.Y, color)
		} else {
			drawBox(screen, p.X, p.Y, color)
		}
	}
}

func buildLogo(wide bool) logo {
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

	seedOff := 7
	hammerOff := image.Pt(0, 6)

	if wide {
		seedOff = 0
		hammerOff = image.Pt(12+seedOff+5, 0)
	}
	logo := logo{
		Bounds: image.Rectangle{Min: image.Pt(10000, 10000)},
	}
	var logoBoxes strings.Builder
	buildBoxes := func(boxes []image.Point, x, y int) {
		for _, b := range boxes {
			b = b.Add(image.Pt(x, y))
			logo.Bounds = logo.Bounds.Union(image.Rectangle{
				Min: image.Pt(b.X, b.Y),
				Max: image.Pt(b.X+1, b.Y+1),
			})
			logoBoxes.WriteByte(byte(b.X))
			logoBoxes.WriteByte(byte(b.Y))
		}
	}
	buildBoxes(S, 0+seedOff, 0)
	buildBoxes(E, 4+seedOff, 0)
	buildBoxes(E, 8+seedOff, 0)
	buildBoxes(D, 12+seedOff, 0)

	buildBoxes(H, 0+hammerOff.X, hammerOff.Y)
	buildBoxes(A, 5+hammerOff.X, hammerOff.Y)
	buildBoxes(M, 10+hammerOff.X, hammerOff.Y)
	buildBoxes(M, 16+hammerOff.X, hammerOff.Y)
	buildBoxes(E, 22+hammerOff.X, hammerOff.Y)
	buildBoxes(R, 26+hammerOff.X, hammerOff.Y)
	logo.Bounds = logo.Bounds.Canon()
	logo.Bounds = image.Rectangle{
		Min: logo.Bounds.Min.Mul(gridSize),
		Max: logo.Bounds.Max.Mul(gridSize),
	}
	logo.boxes = logoBoxes.String()

	return logo
}

func drawBoxes(screen draw.RGBA64Image, boxes []image.Point, x, y int) {
	for _, c := range boxes {
		drawBox(screen, c.X*gridSize+x, c.Y*gridSize+y, white)
	}
}

const boxSize = gridSize

func clearBox(screen draw.RGBA64Image, x, y int, img image.Image) {
	dr := image.Rect(x, y, x+boxSize, y+boxSize)
	imageDraw(screen, dr, img, image.Point{}, draw.Src)
}

func drawBox(screen draw.RGBA64Image, x, y int, img image.Image) {
	const boxSize = gridSize - 1
	dr := image.Rect(x+1, y+1, x+boxSize, y+boxSize)
	imageDraw(screen, dr, img, image.Point{}, draw.Src)
}

func rgb(c uint32) image.Image {
	r := uint8(c >> 16)
	g := uint8(c >> 8)
	b := uint8(c)
	return image.NewUniform(color.RGBA{
		A: 0xff, R: r, G: g, B: b,
	})
}
