package godotnet

import "errors"

var (
	// ErrServerAlreadyRunning is returned by Server.Run if Run has been
	// called on the same Server instance and has not yet returned.
	ErrServerAlreadyRunning = errors.New("godotnet: server already running")

	// ErrUnknownMessageType indicates a received protobuf message has no
	// registered ClientHandler. The server logs and drops such messages.
	ErrUnknownMessageType = errors.New("godotnet: unknown message type")

	// ErrSessionNotFound is returned (or used internally) when a PlayerID
	// does not match any active session.
	ErrSessionNotFound = errors.New("godotnet: session not found")

	// ErrOutboundQueueFull is the disconnect reason recorded when a
	// player's outbound channel overflows.
	ErrOutboundQueueFull = errors.New("godotnet: outbound queue full")

	// ErrHandlerPanic is the disconnect reason recorded when a client
	// handler panics. The library recovers and tears the session down.
	ErrHandlerPanic = errors.New("godotnet: handler panicked")

	// ErrAuthRejected is the generic disconnect reason recorded when
	// Authenticate returns an error.
	ErrAuthRejected = errors.New("godotnet: authentication rejected")

	// ErrMalformedFrame is recorded when the wire payload fails to
	// parse as the expected protobuf type.
	ErrMalformedFrame = errors.New("godotnet: malformed frame")

	// ErrServerMisconfigured is recorded when a required Config field
	// is missing at runtime (e.g. Authenticate is nil but a Login
	// frame arrived).
	ErrServerMisconfigured = errors.New("godotnet: server misconfigured")

	// ErrPingTimeout is recorded when a session is disconnected
	// because it stopped responding to UDP pings.
	ErrPingTimeout = errors.New("godotnet: ping timeout")
)
