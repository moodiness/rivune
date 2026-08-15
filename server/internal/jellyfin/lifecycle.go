package jellyfin

import "context"

// Run maintains compatibility socket and playback deliveries. Cancellation
// closes every socket lease immediately while bounded playback cleanup drains.
// Calling it without playback configured is safe.
func (handler *Handler) Run(ctx context.Context) {
	if handler == nil {
		return
	}
	if handler.bootstrap == nil {
		if handler.playSessions != nil {
			handler.playSessions.run(ctx)
		}
		return
	}
	socketsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		handler.bootstrap.closeAll()
		close(socketsClosed)
	}()
	if handler.playSessions != nil {
		handler.playSessions.run(ctx)
	} else {
		<-ctx.Done()
	}
	<-socketsClosed
}
