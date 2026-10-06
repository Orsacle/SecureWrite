package wipe

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

type Pattern int

const (
	Zero Pattern = iota
	One
	Random
)

func (p Pattern) String() string {
	switch p {
	case Zero:
		return "zero"
	case One:
		return "one"
	default:
		return "random"
	}
}

func Patterns(name string, passes int) ([]Pattern, error) {
	if passes < 1 {
		return nil, fmt.Errorf("passes must be at least 1, got %d", passes)
	}
	var cycle []Pattern
	switch name {
	case "zero":
		cycle = []Pattern{Zero}
	case "one":
		cycle = []Pattern{One}
	case "random":
		cycle = []Pattern{Random}
	case "dod":
		cycle = []Pattern{Zero, One, Random}
	default:
		return nil, fmt.Errorf("unknown pattern %q (want zero, one, random or dod)", name)
	}
	out := make([]Pattern, passes)
	for i := range out {
		out[i] = cycle[i%len(cycle)]
	}
	return out, nil
}

type generator struct {
	block cipher.Block
	nonce uint64
}

func newGenerator() (*generator, error) {
	var seed [40]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, fmt.Errorf("seeding random generator: %w", err)
	}
	block, err := aes.NewCipher(seed[:32])
	if err != nil {
		return nil, err
	}
	return &generator{block: block, nonce: binary.BigEndian.Uint64(seed[32:])}, nil
}

func (g *generator) fill(buf []byte, p Pattern, pass, chunk int, off int64) {
	switch p {
	case Zero:
		clear(buf)
	case One:
		if len(buf) == 0 {
			return
		}
		buf[0] = 0xff
		for n := 1; n < len(buf); n *= 2 {
			copy(buf[n:], buf[:n])
		}
	case Random:
		hi := uint64(pass)<<32 | uint64(chunk)
		lo := g.nonce + uint64(off/aes.BlockSize)
		if lo < g.nonce {
            hi++
		}
		var iv [aes.BlockSize]byte
		binary.BigEndian.PutUint64(iv[:8], hi)
		binary.BigEndian.PutUint64(iv[8:], lo)
		clear(buf)
		cipher.NewCTR(g.block, iv[:]).XORKeyStream(buf, buf)
	}
}
