package tabMapview

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func levelToTankmap(tankmapsPath, level string) (*image.RGBA, error) {
	fname := strings.TrimSuffix(strings.TrimPrefix(level, `levels/`), `.bin`) + `_tankmap.png`
	p := filepath.Join(tankmapsPath, fname)
	f, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			tankmapUrl := `https://raw.githubusercontent.com/LivingTheDagor/WtMiniMapPictures/refs/heads/main/2048/` + fname
			ret, err := http.Get(tankmapUrl)
			if err != nil {
				return nil, fmt.Errorf("failed fetching non cached tankmap %q: %w", tankmapUrl, err)
			}
			if ret.StatusCode != 200 {
				return nil, fmt.Errorf("failed fetching non cached tankmap %q: %s", tankmapUrl, ret.Status)
			}
			defer ret.Body.Close()
			f, err = io.ReadAll(ret.Body)
			if err != nil {
				return nil, fmt.Errorf("failed reading non cached tankmap %q: %s", tankmapUrl, err)
			}
			err = os.MkdirAll(tankmapsPath, 0644)
			if err != nil {
				return nil, fmt.Errorf("can't create tankmap cache dir %q: %s", tankmapsPath, err)
			}
			err = os.WriteFile(p, f, 0644)
			if err != nil {
				return nil, fmt.Errorf("can't write tankmap to cache %q: %s", p, err)
			}
		} else {
			return nil, err
		}
	}
	im, err := png.Decode(bytes.NewReader(f))
	if err != nil {
		return nil, err
	}
	im2, ok := im.(*image.RGBA)
	if !ok {
		im2 = image.NewRGBA(im.Bounds())
		draw.Draw(im2, im2.Bounds(), im, im.Bounds().Min, draw.Src)
	}
	return im2, nil
}

func fallbackTankmap() *image.RGBA {
	const size = 2048
	im := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{R: 22, G: 26, B: 30, A: 255}
	minor := color.RGBA{R: 48, G: 57, B: 66, A: 255}
	major := color.RGBA{R: 82, G: 96, B: 110, A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			im.SetRGBA(x, y, bg)
		}
	}
	for i := 0; i <= size; i += 128 {
		c := minor
		if i%512 == 0 {
			c = major
		}
		for p := 0; p < size; p++ {
			im.SetRGBA(p, i, c)
			im.SetRGBA(i, p, c)
		}
	}
	return im
}
