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

type Data struct {
	Data      []byte
	Threshold int
	Shards    int
}

// Split searches for the appropriate seqNum in the [UR] encoding
// that makes m-of-n backups recoverable regardless of
// which m-sized subset is used. To achieve that, we're exploiting the
// fact that the UR encoding of a fragment can contain multiple fragments,
// xor'ed together.
//
// Schemes are implemented for backups where m == n - 1 and for 3-of-5.
//
// For m == n - 1, the data is split into m parts (seqLen in UR parlor), and m shares have parts
// assigned as follows:
//
//	1, 2, ..., m
//
// The final share contains the xor of all m parts.
//
// The scheme can trivially recover the data when selecting the m shares each with 1
// part. For all other selections, one share will be missing, say k, but we'll have the
// final plate with every part xor'ed together. So, k is derived by xor'ing (canceling) every
// part other than k into the combined part.
//
// Example: a 2-of-3 setup will have data split into 2 parts, with the 3 shares assigned parts
// like so: 1, 2, 1 ⊕ 2. Selecting the first two plates, the data is trivially recovered;
// otherwise we have one part, say 1, and the combined part. The other part, 2, is then recovered
// by xor'ing the one part with the combination: 1 ⊕ 1 ⊕ 2 = 2.
//
// For 3-of-5, the data is split into 6 parts, and each share will have two parts assigned.
//
// The assignment is as follows, where p1 and p2 denotes the two parts assigned to each share.
//
//	share    |    p1     |        p2
//	 1            1         6 ⊕ 5 ⊕ 2
//	 2            2         6 ⊕ 1 ⊕ 3
//	 3            3         6 ⊕ 2 ⊕ 4
//	 4            4         6 ⊕ 3 ⊕ 5
//	 5            5         6 ⊕ 4 ⊕ 1
//
// That is, every share is assigned a part and the combination of the 6 part with the neighbour
// parts.
//
// [UR]: https://github.com/BlockchainCommons/Research/blob/master/papers/bcr-2020-005-ur.md
func Split(data Data, keyIdx int) (urs []string) {
	var shares [][]int
	var seqLen int
	n, m := data.Shards, data.Threshold
	switch {
	case n-m <= 1:
		// Optimal: 1 part per share, seqLen m.
		seqLen = m
		if keyIdx < m {
			shares = [][]int{{keyIdx}}
		} else {
			all := make([]int, 0, m)
			for i := range m {
				all = append(all, i)
			}
			shares = [][]int{all}
		}
	case n == 4 && m == 2:
		// Optimal, but 2 parts per share.
		seqLen = m * 2
		switch keyIdx {
		case 0:
			shares = [][]int{{0}, {1}}
		case 1:
			shares = [][]int{{2}, {3}}
		case 2:
			shares = [][]int{{0, 2}, {1, 3}}
		case 3:
			shares = [][]int{{0, 2, 1}, {1, 3, 2}}
		}
	case n == 5 && m == 3:
		// Optimal, but 2 parts per share. There doesn't seem to exist an
		// optimal scheme with 1 part per share.
		seqLen = m * 2
		second := []int{
			n,
			(keyIdx + n - 1) % n,
			(keyIdx + 1) % n,
		}
		shares = [][]int{{keyIdx}, second}
	default:
		// Fallback: every share contains the complete data. It's only optimal
		// for 1-of-n backups.
		seqLen = 1
		shares = [][]int{{0}}
	}
	check := fountain.Checksum(data.Data)
	for _, frag := range shares {
		seqNum := fountain.SeqNumFor(seqLen, check, frag)
		qr := strings.ToUpper(Encode("crypto-output", data.Data, seqNum, seqLen))
		urs = append(urs, qr)
	}
	return
}

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
