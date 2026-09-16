package lumen

import (
	"errors"
	"fmt"
	"sync"
)

var errReconnectCancelled = errors.New("reconnect cancelled")

type reconnectSnapshot struct {
	threadID             string
	cwd                  string
	overrides            runtimeOverrides
	hasTranscriptContent bool
}

type sessionSupervisor struct {
	factory func() (*appServer, error)
	done    chan struct{}
	results chan reconnectResult

	closeOnce sync.Once
	mu        sync.Mutex
	running   bool
	closed    bool
}

func newSessionSupervisor(factory func() (*appServer, error)) *sessionSupervisor {
	if factory == nil {
		factory = startAppServer
	}
	return &sessionSupervisor{
		factory: factory,
		done:    make(chan struct{}),
		results: make(chan reconnectResult),
	}
}

func (s *sessionSupervisor) Results() <-chan reconnectResult {
	return s.results
}

func (s *sessionSupervisor) Start(snapshot reconnectSnapshot) bool {
	s.mu.Lock()
	if s.closed || s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()

	go func() {
		result := reconnectWithSnapshot(s.factory, snapshot, s.done)

		s.mu.Lock()
		s.running = false
		s.mu.Unlock()

		select {
		case s.results <- result:
		case <-s.done:
			if result.client != nil {
				result.client.Close()
			}
		}
	}()
	return true
}

func (s *sessionSupervisor) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)
	})
}

func reconnectWithSnapshot(factory func() (*appServer, error), snapshot reconnectSnapshot, done <-chan struct{}) reconnectResult {
	if reconnectDone(done) {
		return reconnectResult{err: errReconnectCancelled}
	}
	type factoryResult struct {
		client *appServer
		err    error
	}
	factoryResults := make(chan factoryResult)
	go func() {
		var result factoryResult
		func() {
			defer func() {
				if value := recover(); value != nil {
					result.err = fmt.Errorf("reconnect factory panic: %v", value)
				}
			}()
			result.client, result.err = factory()
		}()
		select {
		case factoryResults <- result:
		case <-done:
			if result.client != nil {
				result.client.Close()
			}
		}
	}()
	var outcome factoryResult
	select {
	case outcome = <-factoryResults:
	case <-done:
		return reconnectResult{err: errReconnectCancelled}
	}
	client, err := outcome.client, outcome.err
	if err == nil && client == nil {
		err = errors.New("reconnect factory returned nil app-server")
	}
	if err == nil {
		err = reconnectCall(done, client.Initialize)
	}
	var thread threadSummary
	if err == nil {
		if snapshot.threadID != "" {
			thread, err = reconnectCallWithThread(done, client, func() (threadSummary, error) {
				return client.ResumeThread(snapshot.threadID, snapshot.overrides)
			})
			if canStartFreshThreadAfterResume(err, snapshot.hasTranscriptContent) {
				thread, err = reconnectCallWithThread(done, client, func() (threadSummary, error) {
					return client.StartThread(snapshot.cwd, snapshot.overrides)
				})
			}
		} else {
			thread, err = reconnectCallWithThread(done, client, func() (threadSummary, error) {
				return client.StartThread(snapshot.cwd, snapshot.overrides)
			})
		}
	}
	if err != nil && client != nil {
		client.Close()
	}
	result := reconnectResult{client: client, thread: thread, err: err}
	if reconnectDone(done) {
		if result.client != nil {
			result.client.Close()
		}
		return reconnectResult{err: errReconnectCancelled}
	}
	return result
}

func reconnectCall(done <-chan struct{}, call func() error) error {
	results := make(chan error, 1)
	go func() {
		var err error
		func() {
			defer func() {
				if value := recover(); value != nil {
					err = fmt.Errorf("reconnect call panic: %v", value)
				}
			}()
			err = call()
		}()
		results <- err
	}()
	select {
	case err := <-results:
		return err
	case <-done:
		return errReconnectCancelled
	}
}

func reconnectCallWithThread(done <-chan struct{}, client *appServer, call func() (threadSummary, error)) (threadSummary, error) {
	results := make(chan struct {
		thread threadSummary
		err    error
	}, 1)
	go func() {
		var result struct {
			thread threadSummary
			err    error
		}
		func() {
			defer func() {
				if value := recover(); value != nil {
					result.err = fmt.Errorf("reconnect call panic: %v", value)
				}
			}()
			result.thread, result.err = call()
		}()
		results <- result
	}()
	select {
	case result := <-results:
		return result.thread, result.err
	case <-done:
		client.Close()
		return threadSummary{}, errReconnectCancelled
	}
}

func reconnectDone(done <-chan struct{}) bool {
	if done == nil {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}
