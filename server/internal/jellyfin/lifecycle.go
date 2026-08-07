package jellyfin

import "context"

// Run maintains compatibility playback deliveries. After ctx is canceled it
// retires the generation only after delivery cleanup succeeds or the bounded
// cleanup retry TTL expires. Calling it without playback configured is safe.
func (handler *Handler) Run(ctx context.Context) {
	if handler == nil || handler.playSessions == nil {
		return
	}
	handler.playSessions.run(ctx)
}
