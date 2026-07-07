package services

import (
	"context"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================
// BOUNDED TASK QUEUE
// Prevents unbounded goroutine creation by using a worker pool
// =============================================

// Task represents a background task to execute
type Task struct {
	Name    string
	Handler func(ctx context.Context) error
}

// TaskQueue manages a bounded pool of workers for background tasks
type TaskQueue struct {
	tasks       chan Task
	workers     int
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
	isRunning   atomic.Bool

	// Metrics
	tasksEnqueued  atomic.Uint64
	tasksCompleted atomic.Uint64
	tasksFailed    atomic.Uint64
	tasksDropped   atomic.Uint64
}

// Global task queue instance
var BackgroundTasks *TaskQueue

// DefaultWorkers returns a reasonable default based on CPU count
func DefaultWorkers() int {
	cpus := runtime.NumCPU()
	workers := cpus * 2 // 2 workers per CPU
	if workers < 4 {
		workers = 4 // Minimum 4 workers
	}
	if workers > 32 {
		workers = 32 // Maximum 32 workers
	}
	return workers
}

// InitTaskQueue initializes the global background task queue
func InitTaskQueue(workers int) {
	if workers <= 0 {
		workers = DefaultWorkers()
	}

	ctx, cancel := context.WithCancel(context.Background())

	BackgroundTasks = &TaskQueue{
		tasks:   make(chan Task, 1000), // Buffer for 1000 pending tasks
		workers: workers,
		ctx:     ctx,
		cancel:  cancel,
	}

	BackgroundTasks.Start()
	log.Printf("[TaskQueue] Initialized with %d workers (queue capacity: 1000)", workers)
}

// Start begins the worker pool
func (q *TaskQueue) Start() {
	if q.isRunning.Swap(true) {
		return // Already running
	}

	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
}

// Stop gracefully shuts down the task queue
func (q *TaskQueue) Stop() {
	if !q.isRunning.Swap(false) {
		return // Already stopped
	}

	q.cancel()
	close(q.tasks)
	q.wg.Wait()
	log.Printf("[TaskQueue] Shutdown complete. Stats: enqueued=%d, completed=%d, failed=%d, dropped=%d",
		q.tasksEnqueued.Load(), q.tasksCompleted.Load(), q.tasksFailed.Load(), q.tasksDropped.Load())
}

// worker processes tasks from the queue
func (q *TaskQueue) worker(id int) {
	defer q.wg.Done()

	for task := range q.tasks {
		select {
		case <-q.ctx.Done():
			return
		default:
		}

		// Execute task with timeout
		taskCtx, cancel := context.WithTimeout(q.ctx, 30*time.Second)

		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[TaskQueue] Worker %d: PANIC in task '%s': %v", id, task.Name, r)
					q.tasksFailed.Add(1)
				}
				cancel()
			}()

			if err := task.Handler(taskCtx); err != nil {
				log.Printf("[TaskQueue] Worker %d: Task '%s' failed: %v", id, task.Name, err)
				q.tasksFailed.Add(1)
			} else {
				q.tasksCompleted.Add(1)
			}
		}()
	}
}

// Enqueue adds a task to the queue
// Returns true if enqueued, false if queue is full
func (q *TaskQueue) Enqueue(name string, handler func(ctx context.Context) error) bool {
	if !q.isRunning.Load() {
		log.Printf("[TaskQueue] WARNING: Queue not running, executing task '%s' synchronously", name)
		// Fallback: execute synchronously with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := handler(ctx); err != nil {
			log.Printf("[TaskQueue] Synchronous task '%s' failed: %v", name, err)
		}
		return true
	}

	q.tasksEnqueued.Add(1)

	select {
	case q.tasks <- Task{Name: name, Handler: handler}:
		return true
	default:
		// Queue is full
		q.tasksDropped.Add(1)
		log.Printf("[TaskQueue] WARNING: Queue full, dropping task '%s'", name)
		return false
	}
}

// EnqueueWithPriority adds a high-priority task (executes before normal tasks)
// NOTE: This is a simple implementation - true priority would require a priority queue
func (q *TaskQueue) EnqueueWithPriority(name string, handler func(ctx context.Context) error) bool {
	// For now, high priority tasks execute synchronously if queue is busy
	select {
	case q.tasks <- Task{Name: name, Handler: handler}:
		q.tasksEnqueued.Add(1)
		return true
	default:
		// Queue is full, execute synchronously for high priority
		log.Printf("[TaskQueue] High priority task '%s' executing synchronously (queue full)", name)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := handler(ctx); err != nil {
			log.Printf("[TaskQueue] High priority task '%s' failed: %v", name, err)
			q.tasksFailed.Add(1)
		} else {
			q.tasksCompleted.Add(1)
		}
		return true
	}
}

// Stats returns current queue statistics
func (q *TaskQueue) Stats() map[string]interface{} {
	return map[string]interface{}{
		"workers":         q.workers,
		"pending":         len(q.tasks),
		"capacity":        cap(q.tasks),
		"enqueued":        q.tasksEnqueued.Load(),
		"completed":       q.tasksCompleted.Load(),
		"failed":          q.tasksFailed.Load(),
		"dropped":         q.tasksDropped.Load(),
		"is_running":      q.isRunning.Load(),
	}
}

// =============================================
// CONVENIENCE FUNCTIONS
// =============================================

// RunBackground is a convenience function to enqueue a task
func RunBackground(name string, handler func(ctx context.Context) error) {
	if BackgroundTasks != nil {
		BackgroundTasks.Enqueue(name, handler)
	} else {
		// Fallback: execute synchronously
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := handler(ctx); err != nil {
			log.Printf("[Background] Task '%s' failed: %v", name, err)
		}
	}
}

// RunBackgroundSimple is for simple fire-and-forget operations that don't need context
func RunBackgroundSimple(name string, handler func()) {
	RunBackground(name, func(_ context.Context) error {
		handler()
		return nil
	})
}
