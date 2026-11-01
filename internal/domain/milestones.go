package domain

// MilestoneKind says how a milestone's month is determined.
type MilestoneKind string

const (
	// MilestoneAge happens when a person reaches an age.
	MilestoneAge MilestoneKind = "age"
	// MilestoneDate happens in a fixed month.
	MilestoneDate MilestoneKind = "date"
	// MilestoneFI (financial independence, FIRE) happens the first month
	// savings, excluding property, reach FIMultiple times a yearly spending
	// figure that depends on the FIVariant. It can only be resolved by running
	// the projection.
	MilestoneFI MilestoneKind = "fi"
)

// MilestoneKinds lists the kinds in display order.
var MilestoneKinds = []MilestoneKind{MilestoneAge, MilestoneDate, MilestoneFI}

// DefaultFIMultiple is the classic "25 times yearly spending" (the 4 % rule).
const DefaultFIMultiple = 25.0

// FIVariant is the flavour of financial independence (FIRE) a milestone uses.
type FIVariant string

const (
	// FIStandard: savings reach N times the actual yearly spending.
	FIStandard FIVariant = "standard"
	// FILean: N times a frugal yearly budget (FIAmount, today's euros).
	FILean FIVariant = "lean"
	// FIFat: N times a generous yearly budget (FIAmount, today's euros).
	FIFat FIVariant = "fat"
	// FIBarista: N times the spending a part-time income (FIAmount per year,
	// today's euros) does not cover.
	FIBarista FIVariant = "barista"
	// FICoast: savings would grow, with no further contributions, to N times
	// the yearly spending by the time the person reaches Age.
	FICoast FIVariant = "coast"
)

// FIVariants lists the variants in display order.
var FIVariants = []FIVariant{FIStandard, FILean, FIFat, FIBarista, FICoast}

// UsesAmount reports whether the variant needs FIAmount.
func (v FIVariant) UsesAmount() bool { return v == FILean || v == FIFat || v == FIBarista }

// Milestone is a life event that income, expenses and contributions can start
// or stop on.
type Milestone struct {
	ID         int64
	PlanID     int64
	Name       string
	Kind       MilestoneKind
	PersonID   *int64    // age milestones
	Age        int       // age milestones
	Date       Month     // date milestones
	FIMultiple float64   // financial independence milestones
	FIVariant  FIVariant // financial independence milestones
	// FIAmount is the yearly budget (lean, fat) or part-time income (barista),
	// in today's euros.
	FIAmount Cents
	// Coast FIRE reuses PersonID and Age as the target person and age.
}

// Conditional reports whether the month is only known after simulating.
func (m Milestone) Conditional() bool { return m.Kind == MilestoneFI }

// FixedMonth returns the month of an age or date milestone, and false for
// conditional milestones or when the person is unknown.
func (m Milestone) FixedMonth(people []Person) (Month, bool) {
	switch m.Kind {
	case MilestoneDate:
		return m.Date, !m.Date.IsZero()
	case MilestoneAge:
		if m.PersonID == nil {
			return Month{}, false
		}
		for _, p := range people {
			if p.ID == *m.PersonID {
				return p.Birth.AddMonths(m.Age * 12), true
			}
		}
	}
	return Month{}, false
}

// Validate checks the milestone. Field names match the milestone form.
func (m Milestone) Validate() Errors {
	errs := Errors{}
	validateName(m.Name, errs)
	switch m.Kind {
	case MilestoneAge:
		if m.PersonID == nil {
			errs.Add("person", ErrRequired)
		}
		if m.Age < 1 || m.Age > 120 {
			errs.Add("age", ErrRange)
		}
	case MilestoneDate:
		if m.Date.IsZero() {
			errs.Add("date", ErrRequired)
		}
	case MilestoneFI:
		if !oneOf(m.FIVariant, FIVariants) {
			errs.Add("fi_variant", ErrChoice)
		}
		if m.FIVariant.UsesAmount() && m.FIAmount <= 0 {
			errs.Add("fi_amount", ErrRequired)
		}
		if m.FIVariant == FICoast {
			if m.PersonID == nil {
				errs.Add("coast_person", ErrRequired)
			}
			if m.Age < 18 || m.Age > 110 {
				errs.Add("coast_age", ErrRange)
			}
		}
		if m.FIMultiple < 5 || m.FIMultiple > 100 {
			errs.Add("multiple", ErrRange)
		}
	default:
		errs.Add("kind", ErrChoice)
	}
	return errs
}

// Resolved maps milestone IDs to the month they happen. A missing entry means
// the milestone has not been reached (or never is).
type Resolved map[int64]Month

// ResolveFixed resolves age and date milestones. Conditional ones are left out;
// the engine adds them as the projection reaches them.
func ResolveFixed(milestones []Milestone, people []Person) Resolved {
	r := Resolved{}
	for _, m := range milestones {
		if month, ok := m.FixedMonth(people); ok {
			r[m.ID] = month
		}
	}
	return r
}

// Window returns the active span of a stream. A start milestone overrides the
// fixed start and is inclusive; an end milestone overrides the fixed end and
// stops the stream the month before it happens. ok is false when the stream
// waits for a milestone that has not been reached, so it is not active at all.
func (r Resolved) Window(start, end Month, startMS, endMS *int64) (s, e Month, ok bool) {
	s, e = start, end
	if startMS != nil {
		m, reached := r[*startMS]
		if !reached {
			return s, e, false
		}
		s = m
	}
	if endMS != nil {
		if m, reached := r[*endMS]; reached {
			e = m.AddMonths(-1)
		} else {
			e = Month{} // never reached: never ends
		}
	}
	return s, e, true
}

// ActiveIn reports whether a stream with these bounds is active in month now.
func (r Resolved) ActiveIn(start, end Month, startMS, endMS *int64, now Month) bool {
	s, e, ok := r.Window(start, end, startMS, endMS)
	return ok && Active(s, e, now)
}

// ContributingIn reports whether an account still receives contributions in
// month now (contributions stop the month its end milestone happens).
func (r Resolved) ContributingIn(a Account, now Month) bool {
	if a.ContributionEndMS == nil {
		return true
	}
	m, reached := r[*a.ContributionEndMS]
	return !reached || now.Before(m)
}
