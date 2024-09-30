//go:build tinygo

package mjolnir

import (
	"errors"
	"io"
)

func Open(dev string) (io.ReadWriteCloser, error) {
	return nil, errors.New("not implemented")
}
