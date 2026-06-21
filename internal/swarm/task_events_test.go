/*
 * Copyright (c) 2026 wellwell.work, LLC by Zoe
 *
 * Licensed under the Apache License 2.0 (the "License");
 * You may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 */

package swarm

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.zoe.im/spore/internal/runtime"
)

func TestTaskEventBroadcaster_BasicFanout(t *testing.T) {
	b := newTaskEventBroadcaster()

	ch1, cancel1 := b.subscribe("task-1")
	defer cancel1()
	ch2, cancel2 := b.subscribe("task-1")
	defer cancel2()

	go b.publish("task-1", runtime.StreamEvent{Type: runtime.EventThinking, Content: "hello"})

	for _, ch := range []<-chan runtime.StreamEvent{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Content != "hello" {
				t.Fatalf("got %q, want hello", ev.Content)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event")
		}
	}
}

func TestTaskEventBroadcaster_IsolatedPerTask(t *testing.T) {
	b := newTaskEventBroadcaster()

	chA, cancelA := b.subscribe("task-a")
	defer cancelA()
	chB, cancelB := b.subscribe("task-b")
	defer cancelB()

	b.publish("task-a", runtime.StreamEvent{Content: "a"})

	select {
	case ev := <-chA:
		if ev.Content != "a" {
			t.Fatalf("chA got %q, want a", ev.Content)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("chA did not receive event")
	}

	select {
	case ev := <-chB:
		t.Fatalf("chB unexpectedly got %v", ev)
	case <-time.After(50 * time.Millisecond):
		// good — no event for task-b
	}
}

func TestTaskEventBroadcaster_CancelStopsDelivery(t *testing.T) {
	b := newTaskEventBroadcaster()
	ch, cancel := b.subscribe("task-1")
	cancel()

	// publish after cancel must not panic and must not deliver
	b.publish("task-1", runtime.StreamEvent{Content: "lost"})

	// channel should be closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed, got value")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected closed channel read to return immediately")
	}
}

func TestTaskEventBroadcaster_CloseTask(t *testing.T) {
	b := newTaskEventBroadcaster()
	ch, _ := b.subscribe("task-1")

	b.closeTask("task-1")

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel closed")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected closed channel to return immediately")
	}

	// idempotent
	b.closeTask("task-1")
}

func TestTaskEventBroadcaster_NoSubscribersIsNoop(t *testing.T) {
	b := newTaskEventBroadcaster()
	// Must not panic; must not block.
	done := make(chan struct{})
	go func() {
		b.publish("nobody", runtime.StreamEvent{Content: "lonely"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish to no subscribers blocked")
	}
}

func TestTaskEventBroadcaster_SlowConsumerDoesNotBlock(t *testing.T) {
	b := newTaskEventBroadcaster()
	ch, cancel := b.subscribe("task-1")
	defer cancel()

	// Don't read from ch — fill its buffer (64) and overflow.
	for i := 0; i < 200; i++ {
		b.publish("task-1", runtime.StreamEvent{Content: "x"})
	}

	// Slow consumer never blocked the publisher (test reaches here in <100ms).
	// Drain some to prove ch is still alive.
	drained := 0
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
			drained++
			if drained > 60 {
				return // got at least most of the buffer
			}
		case <-time.After(100 * time.Millisecond):
			if drained == 0 {
				t.Fatal("expected to drain at least some events")
			}
			return
		}
	}
}

func TestTaskEventBroadcaster_ConcurrentSubAndPublish(t *testing.T) {
	b := newTaskEventBroadcaster()
	var wg sync.WaitGroup
	var received atomic.Int64

	// 10 subscribers, 10 publishers, 100 events each.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := b.subscribe("hot")
			defer cancel()
			done := time.After(500 * time.Millisecond)
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
					received.Add(1)
				case <-done:
					return
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.publish("hot", runtime.StreamEvent{Content: "tick"})
			}
		}()
	}
	wg.Wait()
	if received.Load() == 0 {
		t.Fatal("no subscriber received any event")
	}
}
