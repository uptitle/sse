/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package sse

import (
	"net/url"
	"sync"
	"sync/atomic"
)

// Stream ...
type Stream struct {
	// Specifies the function to run when client subscribe
	OnSubscribe func(streamID string, sub *Subscriber)
	event       chan *Event
	quit        chan struct{}
	register    chan *Subscriber
	deregister  chan *Subscriber
	// Specifies the function to run when client unsubscribe
	OnUnsubscribe   func(streamID string, sub *Subscriber)
	ID              string
	subscribers     []*Subscriber
	Eventlog        EventLog
	quitOnce        sync.Once
	subscriberCount int32
	isAutoStream    bool
	// Enables replaying of Eventlog to newly added subscribers
	AutoReplay bool
}

// newStream returns a new stream
func newStream(id string, buffSize int, replay, isAutoStream bool, onSubscribe, onUnsubscribe func(string, *Subscriber)) *Stream {
	return &Stream{
		ID:            id,
		AutoReplay:    replay,
		subscribers:   make([]*Subscriber, 0),
		isAutoStream:  isAutoStream,
		register:      make(chan *Subscriber),
		deregister:    make(chan *Subscriber),
		event:         make(chan *Event, buffSize),
		quit:          make(chan struct{}),
		Eventlog:      make(EventLog, 0),
		OnSubscribe:   onSubscribe,
		OnUnsubscribe: onUnsubscribe,
	}
}

func (str *Stream) run() {
	go func(str *Stream) {
		for {
			select {
			// Add new subscriber
			case subscriber := <-str.register:
				str.subscribers = append(str.subscribers, subscriber)
				if str.AutoReplay {
					str.Eventlog.Replay(subscriber)
				}

			// Remove closed subscriber
			case subscriber := <-str.deregister:
				i := str.getSubIndex(subscriber)
				if i != -1 {
					str.removeSubscriber(i)
				}

				if str.OnUnsubscribe != nil {
					go str.OnUnsubscribe(str.ID, subscriber)
				}

			// Publish event to subscribers
			case event := <-str.event:
				if str.AutoReplay {
					str.Eventlog.Add(event)
				}
				for i := range str.subscribers {
					str.subscribers[i].connection <- event
				}

			// Shutdown if the server closes
			case <-str.quit:
				// remove connections
				str.removeAllSubscribers()
				return
			}
		}
	}(str)
}

func (str *Stream) close() {
	str.quitOnce.Do(func() {
		close(str.quit)
	})
}

func (str *Stream) getSubIndex(sub *Subscriber) int {
	for i := range str.subscribers {
		if str.subscribers[i] == sub {
			return i
		}
	}
	return -1
}

// addSubscriber will create a new subscriber on a stream
func (str *Stream) addSubscriber(eventid int, url *url.URL) *Subscriber {
	atomic.AddInt32(&str.subscriberCount, 1)
	sub := &Subscriber{
		eventid:    eventid,
		quit:       str.deregister,
		connection: make(chan *Event, 64),
		URL:        url,
	}

	if str.isAutoStream {
		sub.removed = make(chan struct{}, 1)
	}

	str.register <- sub

	if str.OnSubscribe != nil {
		go str.OnSubscribe(str.ID, sub)
	}

	return sub
}

func (str *Stream) removeSubscriber(i int) {
	atomic.AddInt32(&str.subscriberCount, -1)
	close(str.subscribers[i].connection)
	if str.subscribers[i].removed != nil {
		str.subscribers[i].removed <- struct{}{}
		close(str.subscribers[i].removed)
	}
	str.subscribers = append(str.subscribers[:i], str.subscribers[i+1:]...)
}

func (str *Stream) removeAllSubscribers() {
	for i := 0; i < len(str.subscribers); i++ {
		close(str.subscribers[i].connection)
		if str.subscribers[i].removed != nil {
			str.subscribers[i].removed <- struct{}{}
			close(str.subscribers[i].removed)
		}
	}
	atomic.StoreInt32(&str.subscriberCount, 0)
	str.subscribers = str.subscribers[:0]
}

func (str *Stream) getSubscriberCount() int {
	return int(atomic.LoadInt32(&str.subscriberCount))
}
