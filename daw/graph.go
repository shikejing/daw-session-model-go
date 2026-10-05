package daw

import (
	"fmt"
	"sort"
	"strings"
)

// CycleError reports a routing cycle and the tracks involved.
type CycleError struct {
	TrackIDs []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("daw: routing cycle detected involving tracks: %s",
		strings.Join(e.TrackIDs, ", "))
}

// ValidateGraph checks the send graph for cycles, returning a *CycleError
// naming the tracks involved if a cycle exists.
func ValidateGraph(s *Session) error {
	_, err := topoOrder(s)
	return err
}

// topoOrder returns track IDs in render order: a track appears after every
// track it receives sends from (dependencies first). A send to an unknown
// track is an error. Deterministic: ties broken by track ID.
func topoOrder(s *Session) ([]string, error) {
	ids := make([]string, 0, len(s.Tracks))
	known := make(map[string]bool, len(s.Tracks))
	for _, t := range s.Tracks {
		if t == nil {
			continue
		}
		if known[t.ID] {
			return nil, fmt.Errorf("daw: duplicate track ID %q", t.ID)
		}
		known[t.ID] = true
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)

	// edges: dep -> dependents; indeg counts unsatisfied dependencies.
	indeg := make(map[string]int, len(ids))
	dependents := make(map[string][]string, len(ids))
	for _, t := range s.Tracks {
		if t == nil {
			continue
		}
		for _, send := range t.ChainSends() {
			if !known[send.TargetTrackID] {
				return nil, fmt.Errorf("daw: track %q sends to unknown track %q", t.ID, send.TargetTrackID)
			}
			if send.TargetTrackID == t.ID {
				return nil, &CycleError{TrackIDs: []string{t.ID}}
			}
			// Signal flows t -> target, so t must render first:
			// target gains a dependency, t gains a dependent.
			dependents[t.ID] = append(dependents[t.ID], send.TargetTrackID)
			indeg[send.TargetTrackID]++
		}
	}

	// Kahn's algorithm with a sorted ready set for determinism.
	var ready []string
	for _, id := range ids {
		if indeg[id] == 0 {
			ready = append(ready, id)
		}
	}
	var order []string
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		next := dependents[id]
		sort.Strings(next)
		for _, dep := range next {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
			}
		}
	}
	if len(order) != len(ids) {
		// Everything not emitted is part of, or downstream of, a cycle.
		var inCycle []string
		for _, id := range ids {
			if indeg[id] > 0 {
				inCycle = append(inCycle, id)
			}
		}
		return nil, &CycleError{TrackIDs: inCycle}
	}
	return order, nil
}
