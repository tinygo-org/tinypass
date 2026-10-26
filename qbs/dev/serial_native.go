//go:build tinygo && !baremetal && !wasip1 && unix && !qbs_noinput

package dev

import (
	"os"
	"syscall"
)

// Serial is the standard input and output of the program. The runner talks to
// the program through them.
var Serial serial

type serial struct{}

// rx holds bytes read from standard input that have not been returned yet.
var rx []byte

var nonblock bool

// Configure does nothing, there is no baud rate.
func (serial) Configure(baud uint32) {
	if !nonblock {
		syscall.SetNonblock(0, true)
		nonblock = true
	}
}

// Buffered returns the number of bytes that can be read without blocking.
func (serial) Buffered() int {
	if len(rx) == 0 {
		var buf [64]byte
		n, err := syscall.Read(0, buf[:])
		if err == nil && n > 0 {
			rx = append(rx, buf[:n]...)
		}
	}
	return len(rx)
}

func (s serial) ReadByte() (byte, error) {
	if s.Buffered() == 0 {
		return 0, os.ErrDeadlineExceeded
	}
	b := rx[0]
	rx = rx[1:]
	return b, nil
}

func (serial) Write(b []byte) (int, error) {
	return os.Stdout.Write(b)
}

func (s serial) WriteByte(c byte) error {
	_, err := s.Write([]byte{c})
	return err
}
