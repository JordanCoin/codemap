package watch

import (
	"time"

	"github.com/fsnotify/fsnotify"
)

// burstDetector decides when the event loop is inside a checkout storm and
// holds the storm's events until it settles. It never reads the wall clock:
// every method takes `now`, so the loop passes time.Now() and tests pass a
// synthetic clock and get the same decisions every run (#201).
type burstDetector struct {
	arrivals []time.Time      // source-event times inside burstDetectWindow
	queue    []fsnotify.Event // the storm's events, first-seen order, last op per path
	queued   map[string]int   // path -> index into queue
	start    time.Time        // when the current burst opened
}

// observe records one source-event arrival and reports whether the loop is
// in a burst: either one is already open, or more than burstThreshold
// arrivals fall inside burstDetectWindow ending now.
func (b *burstDetector) observe(now time.Time) bool {
	if b.queued != nil {
		return true
	}
	cut := 0
	for cut < len(b.arrivals) && now.Sub(b.arrivals[cut]) > burstDetectWindow {
		cut++
	}
	b.arrivals = append(b.arrivals[cut:], now)
	return len(b.arrivals) > burstThreshold
}

// enqueue adds an event to the open burst, keeping the last op per path, and
// returns how long the loop should wait before flushing: burstSettle of
// quiet, clipped so the burst never stays open past burstCeiling.
func (b *burstDetector) enqueue(event fsnotify.Event, now time.Time) time.Duration {
	if b.queued == nil {
		b.queued = make(map[string]int)
		b.start = now
	}
	if i, ok := b.queued[event.Name]; ok {
		b.queue[i] = event
	} else {
		b.queued[event.Name] = len(b.queue)
		b.queue = append(b.queue, event)
	}
	if remaining := burstCeiling - now.Sub(b.start); remaining < burstSettle {
		return max(remaining, 0)
	}
	return burstSettle
}

// take closes the burst and returns its events; the next observe starts a
// fresh detection window.
func (b *burstDetector) take() []fsnotify.Event {
	events := b.queue
	b.queue, b.queued = nil, nil
	b.arrivals = b.arrivals[:0]
	return events
}

// pending reports whether a burst is open with events waiting.
func (b *burstDetector) pending() bool {
	return len(b.queue) > 0
}
