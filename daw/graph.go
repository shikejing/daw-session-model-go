package daw

import (
	"fmt"
	"strings"
)

// CycleError reports a feedback loop in the send graph.
type CycleError struct {
	TrackIDs []string // tracks involved in the cycle, in path order
}

func (e *CycleError) Error() string {
	return "daw: send graph contains a feedback loop: " + strings.Join(e.TrackIDs, " -> ")
}

// CheckAcyclic verifies the send graph has no cycles. It returns a
// *CycleError naming the involved tracks when a loop exists.
func CheckAcyclic(s *Session) error {
	const (
		white = 0 // unvisited
		gray  = 1 // on current DFS path
		black = 2 // fully explored
	)
	color := map[string]int{}
	var stack []string

	var visit func(id string) error
	visit = func(id string) error {
		color[id] = gray
		stack = append(stack, id)
		t := s.FindTrack(id)
		if t != nil {
			for _, send := range t.Sends {
				switch color[send.TargetID] {
				case gray:
					// Found a back edge: extract the cycle from the stack.
					start := 0
					for i, sid := range stack {
						if sid == send.TargetID {
							start = i
							break
						}
					}
					cyc := append([]string(nil), stack[start:]...)
					cyc = append(cyc, send.TargetID)
					return &CycleError{TrackIDs: cyc}
				case white:
					if err := visit(send.TargetID); err != nil {
						return err
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return nil
	}

	for _, t := range s.Tracks {
		if color[t.ID] == white {
			if err := visit(t.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate checks structural invariants: unique track IDs, sends that point
// at existing tracks, and an acyclic send graph.
func Validate(s *Session) error {
	seen := map[string]bool{}
	for _, t := range s.Tracks {
		if t.ID == "" {
			return fmt.Errorf("daw: track with empty ID")
		}
		if seen[t.ID] {
			return fmt.Errorf("daw: duplicate track ID %q", t.ID)
		}
		seen[t.ID] = true
	}
	for _, t := range s.Tracks {
		for _, send := range t.Sends {
			if !seen[send.TargetID] {
				return fmt.Errorf("daw: track %q sends to unknown track %q", t.ID, send.TargetID)
			}
		}
	}
	return CheckAcyclic(s)
}
