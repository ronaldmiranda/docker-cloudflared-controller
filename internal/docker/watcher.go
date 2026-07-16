package docker

import (
	"context"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

// Watch emits a signal on the returned channel (coalesced with debounce)
// whenever a container lifecycle event may have changed the desired routes.
// It reconnects automatically if the event stream drops, and closes the
// channel when ctx is cancelled.
func (d *Discovery) Watch(ctx context.Context, debounce time.Duration) <-chan struct{} {
	out := make(chan struct{}, 1)

	f := filters.NewArgs()
	f.Add("type", string(events.ContainerEventType))
	for _, action := range []string{"start", "die", "stop", "kill", "destroy", "pause", "unpause"} {
		f.Add("event", action)
	}

	notify := func() {
		select {
		case out <- struct{}{}:
		default: // a reconcile is already pending
		}
	}

	go func() {
		defer close(out)
		var timer *time.Timer
		for ctx.Err() == nil {
			msgs, errs := d.cli.Events(ctx, events.ListOptions{Filters: f})
		stream:
			for {
				select {
				case <-ctx.Done():
					return
				case <-msgs:
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(debounce, notify)
				case err := <-errs:
					if ctx.Err() != nil {
						return
					}
					d.log.Warn("docker event stream error, reconnecting", "error", err)
					break stream
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}()

	return out
}
