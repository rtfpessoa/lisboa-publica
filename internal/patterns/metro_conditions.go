package patterns

import (
	"strings"
)

func (e *engine) updateConditions(r Receipt, t Topology) {
	if e.Conditions == nil {
		e.Conditions = map[string]string{}
	}
	changed := map[string]bool{}
	for _, p := range t.Patterns {
		current := r.condition(p.Route)
		if prior, ok := e.Conditions[p.Route]; ok && prior != current {
			changed[p.Route] = true
		}
		e.Conditions[p.Route] = current
	}
	if len(changed) == 0 {
		return
	}
	for key, g := range e.Groups {
		if changed[g.Route] {
			delete(e.Groups, key)
		}
	}
	for key := range e.Previous {
		parts := strings.Split(key, "|")
		if len(parts) == 3 && changed[t.route(parts[0], parts[2])] {
			delete(e.Previous, key)
		}
	}
	e.Gaps++
}
