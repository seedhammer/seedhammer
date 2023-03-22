// Package ur implements the Uniform Resources (UR) encoding
// specified in [BCR-2020-005].
//
// [BCR-2020-005]: https://github.com/BlockchainCommons/Research/blob/master/papers/bcr-2020-005-ur.md
package ur

import (
	"errors"
	"fmt"
	"strings"

	"seedhammer.com/bc/bytewords"
	"seedhammer.com/bc/fountain"
)

func Encode(_type string, message []byte, seqNum, seqLen int) string {
	if seqLen == 1 {
		return fmt.Sprintf("ur:%s/%s", _type, bytewords.Encode(message))
	}
	data := fountain.Encode(message, seqNum, seqLen)
	return fmt.Sprintf("ur:%s/%d-%d/%s", _type, seqNum, seqLen, bytewords.Encode(data))
}

type Decoder struct {
	typ  string
	data []byte

	fountain fountain.Decoder
}

func (d *Decoder) Progress() float32 {
	if d.data != nil {
		return 1
	}
	return d.fountain.Progress()
}

func (d *Decoder) Result() (string, []byte, error) {
	if d.data != nil {
		return d.typ, d.data, nil
	}
	v, err := d.fountain.Result()
	if v == nil {
		return "", nil, err
	}
	return d.typ, v, err
}

func (d *Decoder) Add(ur string) error {
	ur = strings.ToLower(ur)
	const prefix = "ur:"
	if !strings.HasPrefix(ur, prefix) {
		return errors.New("ur: missing ur: prefix")
	}
	ur = ur[len(prefix):]
	parts := strings.SplitN(ur, "/", 3)
	if len(parts) < 2 {
		return errors.New("ur: incomplete UR")
	}
	typ := parts[0]
	if d.typ != "" && d.typ != typ {
		return errors.New("ur: incompatible fragment")
	}
	d.typ = typ
	var seqAndLen string
	var fragment string
	if len(parts) == 2 {
		fragment = parts[1]
	} else {
		seqAndLen, fragment = parts[1], parts[2]
	}
	enc, err := bytewords.Decode(fragment)
	if err != nil {
		return fmt.Errorf("ur: invalid fragment: %w", err)
	}
	if seqAndLen != "" {
		var seq, n int
		if _, err := fmt.Sscanf(seqAndLen, "%d-%d", &seq, &n); err != nil {
			return fmt.Errorf("ur: invalid sequence %q", seqAndLen)
		}
		if err := d.fountain.Add(enc); err != nil {
			return err
		}
	} else {
		d.data = enc
	}
	return nil
}
