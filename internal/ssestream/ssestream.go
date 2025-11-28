// File adapted from openai-go. See https://github.com/openai/openai-go/blob/main/packages/ssestream/ssestream.go

package ssestream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

type Decoder interface {
	Event() Event
	Next() bool
	Close() error
	Err() error
}

func NewDecoder(res *http.Response) Decoder {
	if res == nil || res.Body == nil {
		return nil
	}

	var decoder Decoder

	scn := bufio.NewScanner(res.Body)
	scn.Buffer(nil, bufio.MaxScanTokenSize<<9)
	decoder = &eventStreamDecoder{rc: res.Body, scn: scn}

	return decoder
}

type Event struct {
	Type string
	Data []byte
}

// A base implementation of a Decoder for text/event-stream.
type eventStreamDecoder struct {
	evt Event
	rc  io.ReadCloser
	scn *bufio.Scanner
	err error
}

func (s *eventStreamDecoder) Next() bool {
	if s.err != nil {
		return false
	}

	event := ""
	data := bytes.NewBuffer(nil)

	for s.scn.Scan() {
		txt := s.scn.Bytes()

		// Dispatch event on an empty line
		if len(txt) == 0 {
			s.evt = Event{
				Type: event,
				Data: data.Bytes(),
			}
			return true
		}

		// Split a string like "event: bar" into name="event" and value=" bar".
		name, value, _ := bytes.Cut(txt, []byte(":"))

		// Consume an optional space after the colon if it exists.
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}

		switch string(name) {
		case "":
			// An empty line in the for ": something" is a comment and should be ignored.
			continue
		case "event":
			event = string(value)
		case "data":
			_, s.err = data.Write(value)
			if s.err != nil {
				break
			}
			_, s.err = data.WriteRune('\n')
			if s.err != nil {
				break
			}
		}
	}

	if s.scn.Err() != nil {
		s.err = s.scn.Err()
	}

	return false
}

func (s *eventStreamDecoder) Event() Event {
	return s.evt
}

func (s *eventStreamDecoder) Close() error {
	return s.rc.Close()
}

func (s *eventStreamDecoder) Err() error {
	return s.err
}

type Stream[T any] struct {
	decoder Decoder
	cur     T
	err     error
	done    bool
}

func NewStream[T any](decoder Decoder, err error) *Stream[T] {
	return &Stream[T]{
		decoder: decoder,
		err:     err,
	}
}

// Next returns false if the stream has ended or an error occurred.
// Call Stream.Current() to get the current value.
// Call Stream.Err() to get the error.
//
//		for stream.Next() {
//			data := stream.Current()
//		}
//
//	 	if stream.Err() != nil {
//			...
//	 	}
func (s *Stream[T]) Next() bool {
	if s.err != nil {
		return false
	}

	for s.decoder.Next() {
		if s.done {
			continue
		}

		if bytes.HasPrefix(s.decoder.Event().Data, []byte("[DONE]")) {
			// In this case we don't break because we still want to iterate through the full stream.
			s.done = true
			continue
		}

		var nxt T

		if s.decoder.Event().Type == "" || !strings.HasPrefix(s.decoder.Event().Type, "thread.") {
			ep := gjson.GetBytes(s.decoder.Event().Data, "error")
			if ep.Exists() {
				s.err = fmt.Errorf("received error while streaming: %s", ep.String())
				return false
			}
			// 使用 decoder 并启用 DisallowUnknownFields
			dec := json.NewDecoder(bytes.NewReader(s.decoder.Event().Data))
			dec.DisallowUnknownFields()
			s.err = dec.Decode(&nxt)
			if s.err != nil {
				return false
			}
			s.cur = nxt
			return true
		} else {
			ep := gjson.GetBytes(s.decoder.Event().Data, "error")
			if ep.Exists() {
				s.err = fmt.Errorf("received error while streaming: %s", ep.String())
				return false
			}
			event := s.decoder.Event().Type
			data := s.decoder.Event().Data
			// 使用 decoder 并启用 DisallowUnknownFields
			wrappedData := []byte(fmt.Sprintf(`{ "event": %q, "data": %s }`, event, data))
			dec := json.NewDecoder(bytes.NewReader(wrappedData))
			dec.DisallowUnknownFields()
			s.err = dec.Decode(&nxt)
			if s.err != nil {
				return false
			}
			s.cur = nxt
			return true
		}
	}

	// decoder.Next() may be false because of an error
	s.err = s.decoder.Err()

	return false
}

func (s *Stream[T]) Current() T {
	return s.cur
}

func (s *Stream[T]) Err() error {
	return s.err
}

func (s *Stream[T]) Close() error {
	if s.decoder == nil {
		// already closed
		return nil
	}
	return s.decoder.Close()
}
