package proxy

import (
	"bufio"
	"io"
	"strings"
)

type SSEEvent struct {
	Event string
	Data  []byte
}

type SSEReader struct {
	reader *bufio.Reader
}

func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{reader: bufio.NewReader(r)}
}

func (s *SSEReader) ReadEvent() (*SSEEvent, error) {
	event := &SSEEvent{Event: "data"}

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if len(line) > 0 {
				line = strings.TrimRight(line, "\r\n")
				s.processLine(event, line)
				if len(event.Data) > 0 {
					return event, nil
				}
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")

		if line == "" {
			if len(event.Data) > 0 || event.Event != "data" {
				return event, nil
			}
			continue
		}

		s.processLine(event, line)
	}
}

func (s *SSEReader) processLine(event *SSEEvent, line string) {
	if strings.HasPrefix(line, "event: ") {
		event.Event = strings.TrimPrefix(line, "event: ")
		return
	}

	if strings.HasPrefix(line, "data:") {
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")
		if len(event.Data) > 0 {
			event.Data = append(event.Data, '\n')
		}
		event.Data = append(event.Data, []byte(data)...)
		return
	}
}
