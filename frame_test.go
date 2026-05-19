package godotnet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteFrame_RoundTrip(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{0x00},
		[]byte("hello world"),
		bytes.Repeat([]byte{0xAB}, 4096),
	}
	for _, payload := range cases {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, payload); err != nil {
			t.Fatalf("WriteFrame(%d bytes): %v", len(payload), err)
		}
		got, err := ReadFrame(&buf, 0)
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("round-trip mismatch: got %v, want %v", got, payload)
		}
	}
}

func TestWriteFrame_HeaderEncodingBigEndian(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if len(raw) != frameHeaderSize+len(payload) {
		t.Fatalf("got %d bytes, want %d", len(raw), frameHeaderSize+len(payload))
	}
	n := binary.BigEndian.Uint32(raw[:frameHeaderSize])
	if n != uint32(len(payload)) {
		t.Errorf("encoded length: got %d, want %d", n, len(payload))
	}
}

func TestReadFrame_TooLarge(t *testing.T) {
	var hdr [frameHeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<20)
	r := bytes.NewReader(hdr[:])
	_, err := ReadFrame(r, 1024)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("got %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrame_MaxLenZeroDisablesCap(t *testing.T) {
	payload := bytes.Repeat([]byte{1}, 5000)
	var buf bytes.Buffer
	if err := WriteFrame(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("payload mismatch")
	}
}

func TestReadFrame_TruncatedHeader(t *testing.T) {
	r := bytes.NewReader([]byte{0x00, 0x00})
	_, err := ReadFrame(r, 0)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("got %v, want ErrUnexpectedEOF", err)
	}
}

func TestReadFrame_CleanEOFOnFirstByte(t *testing.T) {
	r := bytes.NewReader(nil)
	_, err := ReadFrame(r, 0)
	if !errors.Is(err, io.EOF) {
		t.Errorf("got %v, want EOF", err)
	}
}

func TestReadFrame_TruncatedPayload(t *testing.T) {
	var hdr [frameHeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], 100)
	data := append(hdr[:], 0x01, 0x02, 0x03)
	r := bytes.NewReader(data)
	_, err := ReadFrame(r, 0)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("got %v, want ErrUnexpectedEOF", err)
	}
}

func TestReadFrame_ZeroLengthFrame(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, nil); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d bytes, want 0", len(got))
	}
}

type errReader struct{ err error }

func (e errReader) Read(_ []byte) (int, error) { return 0, e.err }

func TestReadFrame_WrappedErrorOnHeader(t *testing.T) {
	sentinel := errors.New("custom read failure")
	_, err := ReadFrame(errReader{err: sentinel}, 0)
	if err == nil || !strings.Contains(err.Error(), "custom read failure") {
		t.Fatalf("expected sentinel in error chain, got %v", err)
	}
}
