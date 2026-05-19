package godotnet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrFrameTooLarge is returned by ReadFrame when the declared frame length
// exceeds the maxLen argument.
var ErrFrameTooLarge = errors.New("godotnet: frame exceeds max length")

const frameHeaderSize = 4

// WriteFrame writes payload to w prefixed with a 4-byte big-endian length.
// A nil or empty payload produces a header-only frame (length=0).
func WriteFrame(w io.Writer, payload []byte) error {
	var hdr [frameHeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("godotnet: write frame header: %w", err)
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("godotnet: write frame payload: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed frame from r and returns its payload.
// If the declared length exceeds maxLen, ErrFrameTooLarge is returned and
// no payload bytes are consumed; the caller should treat that as a hostile
// peer and close the connection. A maxLen of 0 disables the cap.
//
// A clean io.EOF before any header byte is read is returned verbatim
// (wrapped via %w); use errors.Is(err, io.EOF) to detect graceful close.
// A truncated header or payload returns io.ErrUnexpectedEOF.
func ReadFrame(r io.Reader, maxLen uint32) ([]byte, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("godotnet: read frame header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if maxLen != 0 && n > maxLen {
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, n, maxLen)
	}
	if n == 0 {
		return []byte{}, nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("godotnet: read frame payload: %w", err)
	}
	return payload, nil
}
