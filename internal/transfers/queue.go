package transfers

import (
	"context"
	"github.com/google/uuid"
	"s3browser/internal/apperr"
	"sync"
	"time"
)

type Job struct {
	ID          string        `json:"id"`
	Type        string        `json:"type"`
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	Transferred int64         `json:"transferred"`
	Total       *int64        `json:"total"`
	Speed       float64       `json:"speed"`
	Error       *apperr.Error `json:"error"`
}
type Progress func(int64, int64)
type Run func(context.Context, Progress) error
type task struct {
	view     Job
	run      Run
	profiles []string
	cancel   context.CancelFunc
}
type Queue struct {
	mu          sync.Mutex
	jobs        []*task
	concurrency int
	active      int
	closed      bool
	wg          sync.WaitGroup
	filter      *apperr.Filter
	update      func(Job)
}

func NewQueue(filter *apperr.Filter, update func(Job)) *Queue {
	return &Queue{concurrency: 3, filter: filter, update: update, jobs: []*task{}}
}
func snapshot(t *task) Job {
	j := t.view
	if j.Total != nil {
		n := *j.Total
		j.Total = &n
	}
	if j.Error != nil {
		e := *j.Error
		j.Error = &e
	}
	return j
}
func (q *Queue) emit(j Job) {
	if q.update != nil {
		q.update(j)
	}
}
func (q *Queue) List() []Job {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs := make([]Job, 0, len(q.jobs))
	for _, t := range q.jobs {
		jobs = append(jobs, snapshot(t))
	}
	return jobs
}
func (q *Queue) Add(kind, name string, ids []string, run Run) (Job, error) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return Job{}, apperr.New("QueueClosed", "Hàng đợi đã đóng.")
	}
	t := &task{view: Job{ID: uuid.NewString(), Type: kind, Name: name, Status: "queued"}, profiles: append([]string(nil), ids...), run: run}
	q.jobs = append(q.jobs, t)
	j := snapshot(t)
	q.mu.Unlock()
	q.emit(j)
	q.pump()
	return j, nil
}
func (q *Queue) pump() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	type start struct {
		t   *task
		ctx context.Context
		job Job
	}
	starts := []start{}
	for _, t := range q.jobs {
		if q.active >= q.concurrency {
			break
		}
		if t.view.Status != "queued" {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.cancel = cancel
		t.view.Status = "running"
		q.active++
		q.wg.Add(1)
		starts = append(starts, start{t, ctx, snapshot(t)})
	}
	q.mu.Unlock()
	for _, s := range starts {
		q.emit(s.job)
		go q.execute(s.ctx, s.t)
	}
}
func (q *Queue) execute(ctx context.Context, t *task) {
	defer q.wg.Done()
	started := time.Now()
	last := time.Time{}
	progress := func(done, total int64) {
		q.mu.Lock()
		t.view.Transferred = done
		if total >= 0 {
			n := total
			t.view.Total = &n
		}
		t.view.Speed = float64(done) / max(time.Since(started).Seconds(), 0.001)
		publish := time.Since(last) > 120*time.Millisecond
		if publish {
			last = time.Now()
		}
		j := snapshot(t)
		q.mu.Unlock()
		if publish {
			q.emit(j)
		}
	}
	err := ctx.Err()
	if err == nil {
		err = t.run(ctx, progress)
	}
	q.mu.Lock()
	if ctx.Err() != nil {
		t.view.Status = "cancelled"
	} else if err != nil {
		t.view.Status = "failed"
		t.view.Error = q.filter.Describe(err)
	} else {
		t.view.Status = "completed"
	}
	t.cancel()
	t.view.Speed = 0
	q.active--
	j := snapshot(t)
	q.mu.Unlock()
	q.emit(j)
	q.pump()
}
func (q *Queue) find(id string) *task {
	for _, t := range q.jobs {
		if t.view.ID == id {
			return t
		}
	}
	return nil
}
func (q *Queue) Cancel(id string) (bool, error) {
	q.mu.Lock()
	t := q.find(id)
	if t == nil {
		q.mu.Unlock()
		return false, apperr.New("TransferNotFound", "Không tìm thấy tác vụ truyền tệp.")
	}
	switch t.view.Status {
	case "queued":
		t.view.Status = "cancelled"
	case "running":
		t.view.Status = "cancelling"
		t.cancel()
	}
	j := snapshot(t)
	q.mu.Unlock()
	q.emit(j)
	return true, nil
}
func (q *Queue) Retry(id string) (bool, error) {
	q.mu.Lock()
	t := q.find(id)
	if q.closed {
		q.mu.Unlock()
		return false, apperr.New("QueueClosed", "Hàng đợi đã đóng.")
	}
	if t == nil || (t.view.Status != "failed" && t.view.Status != "cancelled") {
		q.mu.Unlock()
		return false, apperr.New("InvalidRetry", "Chỉ thử lại tác vụ đã lỗi hoặc đã hủy.")
	}
	t.view.Status = "queued"
	t.view.Transferred = 0
	t.view.Total = nil
	t.view.Error = nil
	t.view.Speed = 0
	j := snapshot(t)
	q.mu.Unlock()
	q.emit(j)
	q.pump()
	return true, nil
}
func (q *Queue) UsesProfile(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.jobs {
		if t.view.Status != "queued" && t.view.Status != "running" && t.view.Status != "cancelling" {
			continue
		}
		for _, p := range t.profiles {
			if p == id {
				return true
			}
		}
	}
	return false
}
func (q *Queue) SetConcurrency(n int) (int, error) {
	if n < 1 || n > 10 {
		return 0, apperr.New("InvalidConcurrency", "Số tác vụ đồng thời phải từ 1 đến 10.")
	}
	q.mu.Lock()
	q.concurrency = n
	q.mu.Unlock()
	q.pump()
	return n, nil
}
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	updates := []Job{}
	for _, t := range q.jobs {
		switch t.view.Status {
		case "queued":
			t.view.Status = "cancelled"
		case "running":
			t.view.Status = "cancelling"
			t.cancel()
		}
		updates = append(updates, snapshot(t))
	}
	q.mu.Unlock()
	for _, j := range updates {
		q.emit(j)
	}
	q.wg.Wait()
}
