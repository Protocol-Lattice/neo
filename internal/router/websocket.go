package router

import (
	"encoding/json"
	"net/http"

	"github.com/Protocol-Lattice/neo/internal/transport/websocket"
)

func isWebSocketRequest(r *http.Request) bool {
	return websocket.IsRequest(r)
}

func serveWebSocket(w http.ResponseWriter, r *http.Request, stream <-chan any) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeProcedureError(w, NewError(CodeInternal, "websocket upgrade is not supported"))
		return
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	accept := websocket.Accept(r.Header.Get("Sec-WebSocket-Key"))
	if err := websocket.WriteUpgrade(rw, accept); err != nil {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case value, ok := <-stream:
			if !ok {
				_ = websocket.WriteFrame(rw, websocket.OpcodeClose, nil, false)
				_ = rw.Flush()
				return
			}

			payload, err := json.Marshal(Response{Result: value})
			if err != nil {
				return
			}
			if err := websocket.WriteFrame(rw, websocket.OpcodeText, payload, false); err != nil {
				return
			}
			if err := rw.Flush(); err != nil {
				return
			}
		}
	}
}
