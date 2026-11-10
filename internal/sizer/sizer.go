package sizer

import (
	"context"
	"sync"
	"time"

	"s3browser/internal/apperr"
	"s3browser/internal/s3svc"
)

const (
	defaultConcurrency = 2
	runningInterval    = 300 * time.Millisecond
)

type Service interface {
	Walk(ctx context.Context, id, bucket, prefix string, visit func(s3svc.Object) error) error
}

type Event struct {
	ProfileID string `json:"profileId"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	State     string `json:"state"`
	Bytes     int64  `json:"bytes"`
	Objects   int64  `json:"objects"`
	Message   string `json:"message"`
}

type key struct {
	profileID string
	bucket    string
	prefix    string
}

type cachedResult struct {
	bytes   int64
	objects int64
}

type task struct {
	key       key
	state     string
	bytes     int64
	objects   int64
	lastEmit  time.Time
	cancel    context.CancelFunc
	cancelled bool
	stale     bool
}

type Sizer struct {
	mu          sync.Mutex
	service     Service
	filter      *apperr.Filter
	update      func(Event)
	concurrency int
	active      int
	closed      bool
	queue       []*task
	tasks       map[key]*task
	cache       map[key]cachedResult
	pending     []Event
	emitting    bool
	wg          sync.WaitGroup
}

func New(service Service, filter *apperr.Filter, update func(Event)) *Sizer {
	if filter == nil {
		filter = &apperr.Filter{}
	}
	return &Sizer{
		service:     service,
		filter:      filter,
		update:      update,
		concurrency: defaultConcurrency,
		tasks:       map[key]*task{},
		cache:       map[key]cachedResult{},
	}
}

func (s *Sizer) flush() {
	s.mu.Lock()
	if s.emitting {
		s.mu.Unlock()
		return
	}
	s.emitting = true
	for len(s.pending) > 0 {
		ev := s.pending[0]
		s.pending = s.pending[1:]
		s.mu.Unlock()
		if s.update != nil {
			s.update(ev)
		}
		s.mu.Lock()
	}
	s.emitting = false
	s.mu.Unlock()
}

func (s *Sizer) Start(profileID, bucket, prefix string, force bool) (bool, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false, apperr.New("AppClosed", "Ứng dụng đã đóng.")
	}
	k := key{profileID: profileID, bucket: bucket, prefix: prefix}
	if existing, ok := s.tasks[k]; ok && !existing.cancelled {
		if force {
			delete(s.cache, k)
		}
		s.mu.Unlock()
		return true, nil
	}
	if force {
		delete(s.cache, k)
	} else if cached, ok := s.cache[k]; ok {
		s.pending = append(s.pending, Event{
			ProfileID: profileID,
			Bucket:    bucket,
			Prefix:    prefix,
			State:     "done",
			Bytes:     cached.bytes,
			Objects:   cached.objects,
		})
		s.mu.Unlock()
		s.flush()
		return true, nil
	}
	t := &task{key: k, state: "queued"}
	s.tasks[k] = t
	s.queue = append(s.queue, t)
	s.pending = append(s.pending, Event{
		ProfileID: profileID,
		Bucket:    bucket,
		Prefix:    prefix,
		State:     "queued",
		Bytes:     0,
		Objects:   0,
	})
	s.mu.Unlock()
	s.flush()
	s.pump()
	return true, nil
}

func (s *Sizer) cancelTask(t *task) {
	if t.cancelled {
		return
	}
	t.cancelled = true
	if s.tasks[t.key] == t {
		delete(s.tasks, t.key)
	}
	if t.state == "queued" {
		for i, q := range s.queue {
			if q == t {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				break
			}
		}
	}
	if t.cancel != nil {
		t.cancel()
	}
	s.pending = append(s.pending, Event{
		ProfileID: t.key.profileID,
		Bucket:    t.key.bucket,
		Prefix:    t.key.prefix,
		State:     "cancelled",
		Bytes:     t.bytes,
		Objects:   t.objects,
	})
}

func (s *Sizer) Cancel(profileID, bucket, prefix string) (bool, error) {
	k := key{profileID: profileID, bucket: bucket, prefix: prefix}
	s.mu.Lock()
	t, ok := s.tasks[k]
	if !ok || t.cancelled {
		s.mu.Unlock()
		return false, nil
	}
	s.cancelTask(t)
	s.mu.Unlock()
	s.flush()
	return true, nil
}

func (s *Sizer) InvalidateBucket(profileID, bucket string) {
	s.mu.Lock()
	for k := range s.cache {
		if k.profileID == profileID && k.bucket == bucket {
			delete(s.cache, k)
		}
	}
	for k, t := range s.tasks {
		if k.profileID == profileID && k.bucket == bucket && t.state == "running" {
			t.stale = true
		}
	}
	s.mu.Unlock()
}

func (s *Sizer) InvalidateProfile(profileID string) {
	s.mu.Lock()
	for k := range s.cache {
		if k.profileID == profileID {
			delete(s.cache, k)
		}
	}
	var targets []*task
	for _, t := range s.queue {
		if t.key.profileID == profileID && !t.cancelled {
			targets = append(targets, t)
		}
	}
	for _, t := range s.tasks {
		if t.key.profileID == profileID && !t.cancelled {
			targets = append(targets, t)
		}
	}
	for _, t := range targets {
		s.cancelTask(t)
	}
	s.mu.Unlock()
	s.flush()
}

func (s *Sizer) pump() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	type runItem struct {
		ctx context.Context
		t   *task
	}
	var starts []runItem
	for s.active < s.concurrency && len(s.queue) > 0 {
		t := s.queue[0]
		s.queue = s.queue[1:]
		if t.cancelled {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		t.cancel = cancel
		t.state = "running"
		s.active++
		s.wg.Add(1)
		starts = append(starts, runItem{ctx: ctx, t: t})
	}
	s.mu.Unlock()
	for _, item := range starts {
		go s.execute(item.ctx, item.t)
	}
}

func (s *Sizer) execute(ctx context.Context, t *task) {
	defer s.wg.Done()
	err := s.service.Walk(ctx, t.key.profileID, t.key.bucket, t.key.prefix, func(o s3svc.Object) error {
		s.mu.Lock()
		if t.cancelled || ctx.Err() != nil {
			s.mu.Unlock()
			if e := ctx.Err(); e != nil {
				return e
			}
			return context.Canceled
		}
		t.bytes += o.Size
		t.objects++
		now := time.Now()
		if t.lastEmit.IsZero() || now.Sub(t.lastEmit) >= runningInterval {
			t.lastEmit = now
			s.pending = append(s.pending, Event{
				ProfileID: t.key.profileID,
				Bucket:    t.key.bucket,
				Prefix:    t.key.prefix,
				State:     "running",
				Bytes:     t.bytes,
				Objects:   t.objects,
			})
		}
		s.mu.Unlock()
		s.flush()
		return nil
	})
	s.mu.Lock()
	s.active--
	if s.tasks[t.key] == t {
		delete(s.tasks, t.key)
	}
	if !t.cancelled {
		if ctx.Err() != nil {
			s.cancelTask(t)
		} else if err != nil {
			msg := s.filter.Describe(err).Message
			s.pending = append(s.pending, Event{
				ProfileID: t.key.profileID,
				Bucket:    t.key.bucket,
				Prefix:    t.key.prefix,
				State:     "error",
				Bytes:     t.bytes,
				Objects:   t.objects,
				Message:   msg,
			})
		} else {
			if !s.closed && !t.stale {
				s.cache[t.key] = cachedResult{bytes: t.bytes, objects: t.objects}
			}
			s.pending = append(s.pending, Event{
				ProfileID: t.key.profileID,
				Bucket:    t.key.bucket,
				Prefix:    t.key.prefix,
				State:     "done",
				Bytes:     t.bytes,
				Objects:   t.objects,
			})
		}
	}
	t.cancel()
	s.mu.Unlock()
	s.flush()
	s.pump()
}

func (s *Sizer) Close() {
	s.mu.Lock()
	s.closed = true
	for _, t := range s.queue {
		s.cancelTask(t)
	}
	s.queue = nil
	for _, t := range s.tasks {
		s.cancelTask(t)
	}
	s.tasks = map[key]*task{}
	s.cache = map[key]cachedResult{}
	s.mu.Unlock()
	s.flush()
	s.wg.Wait()
	s.flush()
}
