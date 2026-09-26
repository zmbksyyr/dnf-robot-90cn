package s4a21

import (
	"context"
	"errors"
	"fmt"

	"robot/internal/foundation/lockhub"
)

const s4a21PersistenceQueueSize = 64

var errPersistenceExecutorClosed = errors.New("S4A21 persistence executor is closed")

type persistenceJob struct {
	ctx    context.Context
	run    func(context.Context) error
	result chan error
}

type persistenceExecutor struct {
	jobs       chan persistenceJob
	stop       chan struct{}
	workerDone chan struct{}
	stateMu    lockhub.Locker
	closed     bool
}

func newPersistenceExecutor(queueSize int) *persistenceExecutor {
	if queueSize < 1 {
		queueSize = 1
	}
	executor := &persistenceExecutor{
		jobs:       make(chan persistenceJob, queueSize),
		stop:       make(chan struct{}),
		workerDone: make(chan struct{}),
	}
	go executor.run()
	return executor
}

func (e *persistenceExecutor) Do(ctx context.Context, run func(context.Context) error) error {
	if e == nil {
		return errPersistenceExecutorClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	job := persistenceJob{ctx: ctx, run: run, result: make(chan error, 1)}
	e.stateMu.Lock()
	closed := e.closed
	e.stateMu.Unlock()
	if closed {
		return errPersistenceExecutorClosed
	}
	select {
	case e.jobs <- job:
	case <-e.stop:
		return errPersistenceExecutorClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-job.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *persistenceExecutor) Close() {
	if e == nil {
		return
	}
	e.stateMu.Lock()
	if !e.closed {
		e.closed = true
		close(e.stop)
	}
	e.stateMu.Unlock()
	<-e.workerDone
}

func (e *persistenceExecutor) run() {
	defer close(e.workerDone)
	for {
		select {
		case job := <-e.jobs:
			e.execute(job)
		case <-e.stop:
			for {
				select {
				case job := <-e.jobs:
					job.result <- errPersistenceExecutorClosed
				default:
					return
				}
			}
		}
	}
}

func (e *persistenceExecutor) execute(job persistenceJob) {
	if err := job.ctx.Err(); err != nil {
		job.result <- err
		return
	}
	var err error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("S4A21 persistence job panic: %v", recovered)
			}
		}()
		if job.run == nil {
			err = errors.New("S4A21 persistence job is nil")
			return
		}
		err = job.run(job.ctx)
	}()
	job.result <- err
}
