package engine

import "github.com/igomezal/horizonte/internal/domain"

// IsWork reports whether an income stops when the person stops working.
func IsWork(k domain.IncomeKind) bool {
	return k == domain.IncomeSalary || k == domain.IncomeSelfEmployed
}

// StopWorkAt returns a copy of in where work income (salaries and
// self-employment) stops when milestone msID happens, as if the household
// stopped working then. Streams that already end earlier are left alone.
// base is the projection of in, which says when each milestone happens;
// stopping work changes nothing before the milestone, so the month is the
// same in the new projection. changed is false when no income is affected
// (the plan already stops working by then, or the milestone is never reached).
func StopWorkAt(in Input, base Result, msID int64) (out Input, changed bool) {
	out = in
	resolved := domain.Resolved{}
	var at domain.Month
	for _, h := range base.Milestones {
		if h.Reached() {
			resolved[h.Milestone.ID] = h.Month
			if h.Milestone.ID == msID {
				at = h.Month
			}
		}
	}
	if at.IsZero() {
		return in, false
	}
	out.Incomes = append([]domain.Income(nil), in.Incomes...)
	for i, inc := range out.Incomes {
		if !IsWork(inc.Kind) {
			continue
		}
		_, end, ok := resolved.Window(inc.Start, inc.End, inc.StartMS, inc.EndMS)
		if !ok && inc.StartMS != nil {
			// Starts on a milestone the plan never reaches: never paid.
			continue
		}
		if !end.IsZero() && end.Before(at) {
			continue // already stops before the milestone
		}
		// A job that would only start after the milestone is never taken.
		id := msID
		out.Incomes[i].EndMS, out.Incomes[i].End = &id, domain.Month{}
		changed = true
	}
	return out, changed
}
