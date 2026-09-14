package lsp

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// writeFrame writes payload with a Content-Length header, LSP base-protocol
// style: "Content-Length: N\r\n\r\n" followed by the JSON body.
func writeFrame(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one Content-Length-framed message from r. It returns
// io.EOF at a clean end of stream and wraps framing violations in
// ErrProtocol.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			continue // unknown header
		}
		if strings.EqualFold(strings.TrimSpace(line[:i]), "content-length") {
			n, cerr := strconv.Atoi(strings.TrimSpace(line[i+1:]))
			if cerr != nil || n < 0 {
				return nil, fmt.Errorf("%w: bad Content-Length %q", ErrProtocol, line[i+1:])
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("%w: missing Content-Length header", ErrProtocol)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
