package envelope

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxHeaderLine caps a single header line. Without it, a body consisting of one
// endless line with no newline would be buffered until we ran out of memory —
// the decompressed cap alone would not save us, because 20 MiB of RAM per
// request across enough concurrent requests is still a way to kill the gateway.
const maxHeaderLine = 64 << 10 // 64 KiB

// Parse reads an envelope from r, enforcing limits as it streams.
//
// r must be the *decompressed* body; call Decompress first. Items whose type is
// unknown to this build are skipped and counted rather than treated as fatal.
func Parse(r io.Reader, limits Limits) (*Envelope, error) {
	limits = limits.withDefaults()

	// Enforce the decompressed cap on the way in. Reading through this reader
	// means a zip bomb fails after MaxDecompressedBytes, not after it has
	// filled memory.
	capped := &cappedReader{r: r, remaining: limits.MaxDecompressedBytes + 1}
	br := bufio.NewReader(capped)

	env := &Envelope{}

	headerLine, err := readLine(br, -1)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, malformed(-1, "empty body")
		}
		return nil, err
	}
	if err := json.Unmarshal(headerLine, &env.Header); err != nil {
		return nil, malformed(-1, "not valid JSON: %v", err)
	}

	for {
		// An envelope may legally end after any complete item.
		itemLine, err := readLine(br, len(env.Items))
		if errors.Is(err, io.EOF) {
			return env, nil
		}
		if err != nil {
			return nil, err
		}
		// Tolerate a trailing newline rather than rejecting the whole payload
		// over a stray byte an SDK appended.
		if len(strings.TrimSpace(string(itemLine))) == 0 {
			continue
		}

		index := len(env.Items)
		if index >= limits.MaxItems {
			return nil, tooLarge(index, "more than %d items", limits.MaxItems)
		}

		var ih ItemHeader
		if err := json.Unmarshal(itemLine, &ih); err != nil {
			return nil, malformed(index, "item header is not valid JSON: %v", err)
		}
		switch {
		case ih.Length < 0:
			return nil, malformed(index, "negative length %d", ih.Length)
		case ih.Length > limits.MaxItemBytes:
			return nil, tooLarge(index, "payload of %d bytes exceeds the %d byte limit", ih.Length, limits.MaxItemBytes)
		}

		payload := make([]byte, ih.Length)
		if _, err := io.ReadFull(br, payload); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				return nil, malformed(index, "header declared %d bytes but the body ended early", ih.Length)
			}
			return nil, wrapRead(err, index)
		}
		// The newline after a payload is optional — the length already told us
		// where the payload ended — but if one is there we must consume it.
		if b, err := br.Peek(1); err == nil && b[0] == '\n' {
			_, _ = br.Discard(1)
		}

		// An unknown type is skipped, not fatal: this is what lets an SDK add a
		// signal before the gateway learns it, without dropping the items the
		// gateway *does* understand in the same envelope.
		if !ih.Type.Valid() {
			env.Skipped++
			continue
		}

		env.Items = append(env.Items, Item{Type: ih.Type, Payload: payload})
	}
}

// Decompress wraps r according to the Content-Encoding header. An unsupported
// encoding is an error rather than a silent passthrough: treating gzip bytes as
// JSON would produce a confusing parse error instead of an honest one.
func Decompress(r io.Reader, contentEncoding string) (io.ReadCloser, error) {
	switch strings.ToLower(strings.TrimSpace(contentEncoding)) {
	case "", "identity":
		return io.NopCloser(r), nil
	case "gzip":
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, malformed(-1, "body is not valid gzip: %v", err)
		}
		return zr, nil
	default:
		return nil, malformed(-1, "unsupported Content-Encoding %q", contentEncoding)
	}
}

// readLine reads one newline-terminated line, rejecting anything longer than
// maxHeaderLine. item is used only to attribute the error.
func readLine(br *bufio.Reader, item int) ([]byte, error) {
	var (
		line  []byte
		total int
	)
	for {
		chunk, isPrefix, err := br.ReadLine()
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				// A final line with no trailing newline is still a line.
				return line, nil
			}
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			return nil, wrapRead(err, item)
		}
		total += len(chunk)
		if total > maxHeaderLine {
			return nil, tooLarge(item, "header line exceeds %d bytes", maxHeaderLine)
		}
		line = append(line, chunk...)
		if !isPrefix {
			return line, nil
		}
	}
}

// wrapRead turns a read error into a typed one. errLimitExceeded comes from the
// capped reader and must surface as 413, not as a generic read failure.
func wrapRead(err error, item int) error {
	if errors.Is(err, errLimitExceeded) {
		return tooLarge(item, "decompressed body exceeds the limit")
	}
	return fmt.Errorf("read envelope: %w", err)
}

// errLimitExceeded is returned by cappedReader.
var errLimitExceeded = errors.New("limit exceeded")

// cappedReader fails once more than remaining bytes have been read. It is what
// makes the decompressed limit a streaming check rather than a post-hoc one.
type cappedReader struct {
	r         io.Reader
	remaining int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, errLimitExceeded
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining <= 0 && err == nil {
		return n, errLimitExceeded
	}
	return n, err
}
