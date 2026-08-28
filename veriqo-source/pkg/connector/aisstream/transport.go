package aisstream

import "context"

// Transport is the seam between this package's protocol/business logic
// (subscribe, filter, hash, normalize, reconnect) and the actual
// network socket. A real implementation dials url over TLS using
// stdlib net/http's DialContext + a hand-rolled minimal WebSocket
// upgrade/frame codec (or any equivalent stdlib-only mechanism) and
// returns a MessageStream that speaks already-unwrapped text frames.
// Tests instead supply a small in-memory fake — see fakeTransport in
// client_test.go — so this package's tests never touch a real socket.
type Transport interface {
	// Connect dials url and returns a ready-to-use MessageStream, or
	// an error. Connect must respect ctx cancellation.
	Connect(ctx context.Context, url string) (MessageStream, error)
}

// MessageStream is one live connection's read/write surface, already
// abstracted away from WebSocket framing: ReadMessage returns one
// complete text message's bytes, WriteMessage sends one complete text
// message. Both must respect ctx.
type MessageStream interface {
	ReadMessage(ctx context.Context) ([]byte, error)
	WriteMessage(ctx context.Context, data []byte) error
	Close() error
}
