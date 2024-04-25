package wrr

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	handlerAny = ""
)

func TestBalancerWeights(t *testing.T) {
	b := New(nil, false)
	addDummyHandler(b, "A", 3)
	addDummyHandler(b, "B", 1)

	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 1, "B": 0})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 2, "B": 0})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 2, "B": 1})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 3, "B": 1})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 4, "B": 1})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 5, "B": 1})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 5, "B": 2})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 6, "B": 2})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 7, "B": 2})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 8, "B": 2})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 8, "B": 3})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 9, "B": 3})
	assertRelease(t, b, "B", map[string]int{"A": 9, "B": 2})
	assertRelease(t, b, "B", map[string]int{"A": 9, "B": 1})
	assertRelease(t, b, "B", map[string]int{"A": 9, "B": 0})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 9, "B": 1})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 9, "B": 2})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 9, "B": 3})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 10, "B": 3})
}

func TestBalancerUpAndDown(t *testing.T) {
	b := New(nil, false)
	addDummyHandler(b, "A", 1)
	addDummyHandler(b, "B", 1)

	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 1, "B": 0})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 1, "B": 1})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 2, "B": 1})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 2, "B": 2})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 3, "B": 2})
	b.SetStatus(context.Background(), "B", false)
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 4, "B": 2})
	b.SetStatus(context.Background(), "B", false)
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 5, "B": 2})
	b.SetStatus(context.Background(), "A", false)
	_, err := b.acquireHandler(handlerAny)
	assert.Equal(t, errNoAvailableServer, err)
	assertRelease(t, b, "B", map[string]int{"A": 5, "B": 1})
	assertRelease(t, b, "A", map[string]int{"A": 4, "B": 1})
	assertRelease(t, b, "A", map[string]int{"A": 3, "B": 1})
	_, err = b.acquireHandler(handlerAny)
	assert.Equal(t, errNoAvailableServer, err)
	b.SetStatus(context.Background(), "A", true)
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 4, "B": 1})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 5, "B": 1})
	b.SetStatus(context.Background(), "B", true)
	b.SetStatus(context.Background(), "B", true)
	b.SetStatus(context.Background(), "A", true)
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 5, "B": 2})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 5, "B": 3})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 5, "B": 4})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 5, "B": 5})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 6, "B": 5})
}

func TestBalancerZeroWeight(t *testing.T) {
	b := New(nil, false)
	addDummyHandler(b, "A", 0)
	addDummyHandler(b, "B", 1)

	assertAcquire(t, b, handlerAny, "B", map[string]int{"B": 1})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"B": 2})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"B": 3})
}

func TestBalancerPropagate(t *testing.T) {
	b := New(nil, true)
	addDummyHandler(b, "A", 1)
	addDummyHandler(b, "B", 1)
	updates := []bool{}
	err := b.RegisterStatusUpdater(func(healthy bool) {
		updates = append(updates, healthy)
	})
	require.NoError(t, err)

	b.SetStatus(context.Background(), "A", false)
	assert.Equal(t, []bool{}, updates)
	b.SetStatus(context.Background(), "A", false)
	assert.Equal(t, []bool{}, updates)
	b.SetStatus(context.Background(), "B", false)
	assert.Equal(t, []bool{false}, updates)
	b.SetStatus(context.Background(), "A", false)
	assert.Equal(t, []bool{false}, updates)
	b.SetStatus(context.Background(), "B", false)
	assert.Equal(t, []bool{false}, updates)
	b.SetStatus(context.Background(), "B", true)
	assert.Equal(t, []bool{false, true}, updates)
	b.SetStatus(context.Background(), "B", true)
	assert.Equal(t, []bool{false, true}, updates)
	b.SetStatus(context.Background(), "A", true)
	assert.Equal(t, []bool{false, true}, updates)
	b.SetStatus(context.Background(), "A", false)
	assert.Equal(t, []bool{false, true}, updates)
	b.SetStatus(context.Background(), "A", false)
	assert.Equal(t, []bool{false, true}, updates)
	b.SetStatus(context.Background(), "B", false)
	assert.Equal(t, []bool{false, true, false}, updates)
}

func TestBalancerSticky(t *testing.T) {
	b := New(nil, false)
	addDummyHandler(b, "A", 1)
	addDummyHandler(b, "B", 1)

	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 1, "B": 0})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 1, "B": 1})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 2, "B": 1})
	assertAcquire(t, b, "A", "A", map[string]int{"A": 3, "B": 1})
	assertAcquire(t, b, "A", "A", map[string]int{"A": 4, "B": 1})
	assertAcquire(t, b, "A", "A", map[string]int{"A": 5, "B": 1})
	b.SetStatus(context.Background(), "A", false)
	// Even though A is preferred B is allocated when A is not available.
	assertAcquire(t, b, "A", "B", map[string]int{"A": 5, "B": 2})
	assertAcquire(t, b, "A", "B", map[string]int{"A": 5, "B": 3})
	b.SetStatus(context.Background(), "A", true)
	assertAcquire(t, b, "A", "A", map[string]int{"A": 6, "B": 3})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 6, "B": 4})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 6, "B": 5})
	assertAcquire(t, b, handlerAny, "B", map[string]int{"A": 6, "B": 6})
	assertAcquire(t, b, handlerAny, "A", map[string]int{"A": 7, "B": 6})
}

// When sticky sessions are allocated that does not mess up selection order.
// Internally heap is used and sticky allocation has to maintain correct
// ordering of handlers in the priority queue.
func TestBalancerMany(t *testing.T) {
	b := New(nil, false)
	for _, handlerName := range "ABCDEFGH" {
		addDummyHandler(b, fmt.Sprintf("%c", handlerName), 1)
	}
	for i := 0; i < 100; i++ {
		_, err := b.acquireHandler(handlerAny)
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]int{"A": 13, "B": 13, "C": 12, "D": 13, "E": 12, "F": 12, "G": 12, "H": 13}, pendingCounts(b))
	for i := 0; i < 10; i++ {
		_, err := b.acquireHandler("D")
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]int{"A": 13, "B": 13, "C": 12, "D": 23, "E": 12, "F": 12, "G": 12, "H": 13}, pendingCounts(b))
	for i := 0; i < 74; i++ {
		_, err := b.acquireHandler(handlerAny)
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]int{"A": 23, "B": 23, "C": 23, "D": 23, "E": 23, "F": 23, "G": 23, "H": 23}, pendingCounts(b))
	for i := 0; i < 8; i++ {
		_, err := b.acquireHandler(handlerAny)
		require.NoError(t, err)
	}
	assert.Equal(t, map[string]int{"A": 24, "B": 24, "C": 24, "D": 24, "E": 24, "F": 24, "G": 24, "H": 24}, pendingCounts(b))
}

func addDummyHandler(b *Balancer, handlerName string, weight int) {
	h := func(rw http.ResponseWriter, req *http.Request) {
		rw.Header().Set("server", handlerName)
		rw.WriteHeader(http.StatusOK)
	}
	b.Add(handlerName, http.HandlerFunc(h), &weight)
}

func pendingCounts(b *Balancer) map[string]int {
	countsByName := make(map[string]int)
	b.mutex.Lock()
	for handlerName, handler := range b.handlersByName {
		countsByName[handlerName] = int(handler.pending) - 1
	}
	b.mutex.Unlock()
	return countsByName
}

func assertAcquire(t *testing.T, b *Balancer, preferredName, acquiredName string, want map[string]int) {
	nh, err := b.acquireHandler(preferredName)
	require.NoError(t, err)
	assert.Equal(t, acquiredName, nh.name)
	assert.Equal(t, want, pendingCounts(b))
}

func assertRelease(t *testing.T, b *Balancer, acquiredName string, want map[string]int) {
	b.mutex.Lock()
	nh := b.handlersByName[acquiredName]
	b.mutex.Unlock()
	b.releaseHandler(nh)
	assert.Equal(t, want, pendingCounts(b))
}
