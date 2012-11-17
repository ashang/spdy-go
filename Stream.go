
package myspdy

import (
    "code.google.com/p/go.net/spdy"
    "io"
    "bufio"
    "net/http"
)



type Receiver interface {
    Receive() (*[]byte, *http.Header, error)
}

type Sender interface {
    Send(*[]byte) error
}




/*
** Stream-specific operations
** (FIXME: wrap in a Stream type?)
**
*/

type Stream struct {
    session         *Session
    Id              uint32
    Input           *StreamReader
    Output          *StreamWriter
    IsMine          bool // Was this stream created locally?
}

func newStream(session *Session, id uint32, IsMine bool) *Stream {
    stream := &Stream{
        session,
        id,
        nil,
        nil,
        IsMine,
    }
    stream.Input = &StreamReader{stream, http.Header{}, NewMQ()}
    stream.Output = &StreamWriter{stream, http.Header{}, 0}
    return stream
}


/*
** StreamReader: read data and headers from a stream
*/


type StreamReader struct {
    stream  *Stream
    headers http.Header
    data    *MQ
}

type streamMessage struct {
    data    *[]byte
    headers *http.Header
}


func (reader *StreamReader) Headers() *http.Header {
    return &reader.headers
}

func (reader *StreamReader) Receive() (*[]byte, *http.Header, error) {
    msg, err := reader.data.Receive()
    return msg.(*streamMessage).data, msg.(*streamMessage).headers, err
}

/* Receive and discard all incoming messages until `headerName` is set, then return its value */
func (reader *StreamReader) WaitForHeader(key string) (string, error) {
    for reader.Headers().Get(key) == "" {
        _, _, err := reader.Receive()
        if err != nil {
            return "", err
        }
    }
    return reader.Headers().Get(key), nil
}


func (reader *StreamReader) Push(data *[]byte, headers *http.Header) {
    reader.data.Send(&streamMessage{data, headers})
}


/* Send all output from a reader to another writer */

func (reader *StreamReader) Pipe(writer *StreamWriter) error {
    for {
        data, headers, err := reader.Receive()
        if err == io.EOF {
            // FIXME: close stream
            // FIXME: expose FLAG_FIN?
            return nil
        } else if err != nil {
            return err
        } else if headers != nil { // FIXME: can data and headers both be non-nil?
            updateHeaders(writer.Headers(), headers)
            writer.SendHeaders(false)
        } else if data != nil {
            writer.Send(data)
        }
    }
    return nil
}


/*
** StreamWriter: write data and headers to a stream
*/


type StreamWriter struct {
    stream      *Stream
    headers     http.Header
    nFramesSent uint32
}

func (writer *StreamWriter) Send(data *[]byte) error {
    if writer.nFramesSent == 0 {
        debug("Calling SendHeaders() before sending data\n")
        writer.SendHeaders(false)
    }
    debug("Sending data: %s\n", data)
    return writer.stream.session.WriteFrame(&spdy.DataFrame{
        StreamId:   writer.stream.Id,
        Data:       *data,
    })
}

func (writer *StreamWriter) SendLines(lines *bufio.Reader) error {
    debug("Sending lines\n")
    for {
        line, _, err := lines.ReadLine()
        if err == io.EOF {
            debug("Received EOF from input\n")
            return nil
        } else if err != nil {
            return err
        }
        if writer.Send(&line) != nil {
            return err
        }
   }
   return nil
}

func (writer *StreamWriter) Headers() *http.Header {
    return &writer.headers
}

func (writer *StreamWriter) SendHeaders(final bool) error {
    // FIXME
    // Optimization: don't resend all headers every time
    var flags spdy.ControlFlags
    if final {
        // If final == true, send FIN flag to close the connection
        flags |= spdy.ControlFlagFin
    }
    if writer.nFramesSent == 0 {
        if writer.stream.IsMine {
            // If this is the first message we send and we initiated the stream, send SYN_STREAM + headers
            err := writer.writeFrame(&spdy.SynStreamFrame{
                    StreamId: writer.stream.Id,
                    CFHeader: spdy.ControlFrameHeader{Flags: flags},
                    Headers: writer.headers,
            })
            if err != nil {
                return err
            }
        } else {
            // If this is the first message we send and we didn't initiate the stream, send SYN_REPLY + headers
            err := writer.writeFrame(&spdy.SynReplyFrame{
                CFHeader:       spdy.ControlFrameHeader{Flags: flags},
                Headers:        writer.headers,
                StreamId:       writer.stream.Id,
            })
            if err != nil {
                return err
            }
        }
    } else {
        // If this is not the first message we send, send HEADERS + headers
        err := writer.writeFrame(&spdy.HeadersFrame{
            CFHeader:       spdy.ControlFrameHeader{Flags: flags},
            Headers:        writer.headers,
            StreamId:       writer.stream.Id,
        })
        if err != nil {
            return err
        }
    }
    return nil
}

func (writer *StreamWriter) writeFrame(frame spdy.Frame) error {
    debug("Writing frame %s", writer)
    err := writer.stream.session.WriteFrame(frame)
    if err != nil {
        return err
    }
    writer.nFramesSent += 1
    return nil
}


func (stream *Stream) Session() *Session {
    // FIXME: simpler to just expose the field
    return stream.session
}

