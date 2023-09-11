package wrr

import (
	"container/heap"
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/traefik/traefik/v2/pkg/config/dynamic"
	"github.com/traefik/traefik/v2/pkg/config/runtime"
	"github.com/traefik/traefik/v2/pkg/log"
)

type namedHandler struct {
	http.Handler
	name     string
	weight   float64
	pending  uint64
	healthy  bool
	queueIdx int
}

type stickyCookie struct {
	name     string
	secure   bool
	httpOnly bool
}

// Balancer is a WeightedRoundRobin load balancer based on Earliest Deadline First (EDF).
// (https://en.wikipedia.org/wiki/Earliest_deadline_first_scheduling)
// Each pick from the schedule has the earliest deadline entry selected.
// Entries have deadlines set at currentDeadline + 1 / weight,
// providing weighted round-robin behavior with floating point weights and an O(log n) pick time.
type Balancer struct {
	stickyCookie     *stickyCookie
	wantsHealthCheck bool
	// updaters is the list of hooks that are run (to update the Balancer
	// parent(s)), whenever the Balancer status changes.
	updaters []func(bool)

	mutex           sync.RWMutex
	enabledHandlers priorityQueue
	handlersByName  map[string]*namedHandler
	healthyCount    int
}

// New creates a new load balancer.
func New(sticky *dynamic.Sticky, wantHealthCheck bool) *Balancer {
	balancer := &Balancer{
		handlersByName:   make(map[string]*namedHandler),
		wantsHealthCheck: wantHealthCheck,
	}
	if sticky != nil && sticky.Cookie != nil {
		balancer.stickyCookie = &stickyCookie{
			name:     sticky.Cookie.Name,
			secure:   sticky.Cookie.Secure,
			httpOnly: sticky.Cookie.HTTPOnly,
		}
	}
	return balancer
}

// SetStatus sets on the balancer that its given child is now of the given
// status.
func (b *Balancer) SetStatus(ctx context.Context, childName string, healthy bool) {
	log.FromContext(ctx).Debugf("Setting status of %s to %v", childName, statusAsStr(healthy))

	b.mutex.Lock()
	nh := b.handlersByName[childName]
	if nh == nil {
		b.mutex.Unlock()
		return
	}

	healthyBefore := b.healthyCount > 0
	if nh.healthy != healthy {
		nh.healthy = healthy
		if healthy {
			b.healthyCount++
			b.enabledHandlers.push(nh)
		} else {
			b.healthyCount--
		}
	}
	healthyAfter := b.healthyCount > 0
	b.mutex.Unlock()

	// No Status Change
	if healthyBefore == healthyAfter {
		// We're still with the same status, no need to propagate
		log.FromContext(ctx).Debugf("Still %s, no need to propagate", statusAsStr(healthyBefore))
		return
	}

	// Status Change
	log.FromContext(ctx).Debugf("Propagating new %s status", statusAsStr(healthyAfter))
	for _, fn := range b.updaters {
		fn(healthyAfter)
	}
}

func statusAsStr(healthy bool) string {
	if healthy {
		return runtime.StatusUp
	}
	return runtime.StatusDown
}

// RegisterStatusUpdater adds fn to the list of hooks that are run when the
// status of the Balancer changes.
// Not thread safe.
func (b *Balancer) RegisterStatusUpdater(fn func(up bool)) error {
	if !b.wantsHealthCheck {
		return errors.New("healthCheck not enabled in config for this weighted service")
	}
	b.updaters = append(b.updaters, fn)
	return nil
}

var errNoAvailableServer = errors.New("no available server")

func (b *Balancer) acquireHandler(preferredName string) (*namedHandler, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	var nh *namedHandler
	// Check the preferred handler fist if provided.
	if preferredName != "" {
		nh = b.handlersByName[preferredName]
		if nh != nil && nh.healthy {
			nh.pending++
			b.enabledHandlers.fix(nh)
			return nh, nil
		}
	}
	// Pick the handler with the least number of pending requests.
	for {
		nh = b.enabledHandlers.pop()
		if nh == nil {
			return nil, errNoAvailableServer
		}
		// If the handler is marked as unhealthy, then continue with the next
		// best option. It will be put back into the priority queue once its
		// status changes to healthy.
		if !nh.healthy {
			continue
		}
		// Otherwise increment the number of pending requests, put it back into
		// the priority queue, and return it as a selected for the request.
		nh.pending++
		b.enabledHandlers.push(nh)
		log.WithoutContext().Debugf("Service selected by WRR: %s", nh.name)
		return nh, nil
	}
}

func (b *Balancer) releaseHandler(nh *namedHandler) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	nh.pending--
	if nh.healthy {
		b.enabledHandlers.fix(nh)
	}
}

func (b *Balancer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var preferredName string
	if b.stickyCookie != nil {
		cookie, err := req.Cookie(b.stickyCookie.name)
		if err != nil && !errors.Is(err, http.ErrNoCookie) {
			log.WithoutContext().Warnf("Error while reading cookie: %v", err)
		}
		if err == nil && cookie != nil {
			preferredName = cookie.Value
		}
	}
	nh, err := b.acquireHandler(preferredName)
	if err != nil {
		if errors.Is(err, errNoAvailableServer) {
			http.Error(w, errNoAvailableServer.Error(), http.StatusServiceUnavailable)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	log.WithoutContext().Infof("Acquired handler: %+v\n", nh)

	if b.stickyCookie != nil {
		cookie := &http.Cookie{
			Name:     b.stickyCookie.name,
			Value:    nh.name,
			Path:     "/",
			HttpOnly: b.stickyCookie.httpOnly,
			Secure:   b.stickyCookie.secure,
		}
		http.SetCookie(w, cookie)
	}

	nh.ServeHTTP(w, req)
	b.releaseHandler(nh)
}

// Add adds a handler.
// A handler with a non-positive weight is ignored.
func (b *Balancer) Add(name string, handler http.Handler, weight *int) {
	w := 1
	if weight != nil {
		w = *weight
	}

	if w <= 0 { // non-positive weight is meaningless
		return
	}

	nh := &namedHandler{
		Handler: handler,
		name:    name,
		weight:  float64(w),
		pending: 1,
		healthy: true,
	}
	b.mutex.Lock()
	b.enabledHandlers.push(nh)
	b.handlersByName[nh.name] = nh
	b.healthyCount++
	b.mutex.Unlock()
}

type priorityQueue struct {
	heap []*namedHandler
}

func (pq *priorityQueue) push(nh *namedHandler) {
	heap.Push(pq, nh)
}

func (pq *priorityQueue) pop() *namedHandler {
	if len(pq.heap) < 1 {
		return nil
	}
	return heap.Pop(pq).(*namedHandler)
}

func (pq *priorityQueue) fix(nh *namedHandler) {
	heap.Fix(pq, nh.queueIdx)
}

// Len implements heap.Interface/sort.Interface.
func (pq *priorityQueue) Len() int { return len(pq.heap) }

// Less implements heap.Interface/sort.Interface.
func (pq *priorityQueue) Less(i, j int) bool {
	nhi, nhj := pq.heap[i], pq.heap[j]
	return float64(nhi.pending)/nhi.weight < float64(nhj.pending)/nhj.weight
}

// Swap implements heap.Interface/sort.Interface.
func (pq *priorityQueue) Swap(i, j int) {
	pq.heap[i], pq.heap[j] = pq.heap[j], pq.heap[i]
	pq.heap[i].queueIdx = i
	pq.heap[j].queueIdx = j
}

// Push implements heap.Interface for pushing an item into the heap.
func (pq *priorityQueue) Push(x interface{}) {
	nh := x.(*namedHandler)
	nh.queueIdx = len(pq.heap)
	pq.heap = append(pq.heap, nh)
}

// Pop implements heap.Interface for popping an item from the heap.
// It panics if b.Len() < 1.
func (pq *priorityQueue) Pop() interface{} {
	lastIdx := len(pq.heap) - 1
	nh := pq.heap[lastIdx]
	pq.heap = pq.heap[0:lastIdx]
	return nh
}
