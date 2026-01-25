package connectplugin

import (
	"context"

	"connectrpc.com/connect"
)

// PumpToStream sends channel values to a Connect server stream until
// the channel closes or an error occurs.
//
// This implements the server streaming adapter pattern from design-uxvj.
//
// Contract:
//   - ch: Entries channel (implementation sends values here)
//   - errs: Error channel (buffered size 1, implementation sends at most one error)
//   - stream: Connect server stream to send to
//
// Returns:
//   - nil when ch closes with no error
//   - error from errs channel if implementation sends one
//   - ctx.Err() if context is cancelled
//   - stream.Send() error if sending fails
//
// Implementation must:
//   - Close ch when done (success or error)
//   - Send at most ONE error to errs
//   - Respect context cancellation
func PumpToStream[T any](
	ctx context.Context,
	ch <-chan T,
	errs <-chan error,
	stream *connect.ServerStream[T],
) error {
	for {
		select {
		case <-ctx.Done():
			// Context cancelled - clean shutdown
			return ctx.Err()

		case err := <-errs:
			// Error from implementation
			// Implementation MUST close ch after sending error
			if err != nil {
				return err
			}

		case msg, ok := <-ch:
			if !ok {
				// Channel closed - normal completion
				return nil
			}
			if err := stream.Send(&msg); err != nil {
				// Stream send failed
				return err
			}
		}
	}
}
