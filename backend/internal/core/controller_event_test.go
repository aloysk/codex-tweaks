package core

import (
	"sync"
	"testing"
)

func TestControllerQueuedEventsPublishCurrentAppearanceRevision(t *testing.T) {
	var publicationsMu sync.Mutex
	var revisions []uint64
	c := runtimeTestController(t, &runtimeTestPlatform{}, &controllerTestCDP{}, func(snapshot AppSnapshot) {
		publicationsMu.Lock()
		revisions = append(revisions, snapshot.Appearance.Revision)
		publicationsMu.Unlock()
	})
	publicationsMu.Lock()
	revisions = nil
	publicationsMu.Unlock()

	// Simulate transport backpressure while newer appearance state arrives.
	// A queued publication must capture state when it obtains the publisher,
	// rather than carrying an earlier draft over a completed cancel/restore.
	c.eventMu.Lock()
	const updates = 64
	updated := make(chan struct{}, updates)
	var workers sync.WaitGroup
	for range updates {
		workers.Add(1)
		go func() {
			defer workers.Done()
			c.mu.Lock()
			c.appearanceRevision++
			c.mu.Unlock()
			updated <- struct{}{}
			c.emit()
		}()
	}
	for range updates {
		<-updated
	}
	c.eventMu.Unlock()
	workers.Wait()
	publicationsMu.Lock()
	defer publicationsMu.Unlock()
	if len(revisions) != 1 || revisions[0] != updates {
		t.Fatalf("queued events published obsolete state instead of coalescing to the current revision: %v", revisions)
	}
}
