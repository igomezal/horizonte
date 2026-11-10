package web

import (
	"context"
	"net/http"

	"github.com/igomezal/horizonte/internal/domain"
	"github.com/igomezal/horizonte/internal/engine"
	"github.com/igomezal/horizonte/internal/i18n"
)

// homeData is the home page's view: the compound-interest example (the hero
// for a plan with nobody in it) and, when the active plan has people, the
// "plan at a glance" card that replaces it.
type homeData struct {
	demoData
	Glance *glanceView
}

// glanceView is the card with the person's own numbers. Everything except the
// Monte Carlo odds comes from the same calls as the pages the tiles link to.
type glanceView struct {
	PlanName string
	NetWorth domain.Cents // as the Finances summary shows it

	// How long the money lasts: the deterministic projection of /plan.
	Failed bool
	Age    int // age of the first person when the money runs out, or at the end

	// The next milestone by date (milestones, paid-off debts and property
	// sales: the dated events of the Plan timeline). HasNext is false when none is upcoming.
	HasNext   bool
	NextLabel string
	NextMonth domain.Month

	Odds glanceOdds
	Chip string // "updating", "critical" or "live"
}

// glanceOdds is the Monte Carlo success rate of /success, which loads after
// the page unless its result is already cached.
type glanceOdds struct {
	Pending bool
	Rate    float64
	Chip    string
	OOB     bool // rendered as the lazy response: the chip swaps along with the tile
}

// glanceChip is the card's status chip: calculating while the odds load, then
// "shortfall" if the plan runs out, otherwise ready.
func glanceChip(failed, pending bool) string {
	switch {
	case pending:
		return "updating"
	case failed:
		return "critical"
	}
	return "live"
}

// nextMilestone is the earliest upcoming dated event of the Plan timeline:
// a milestone, a debt paid off or a property sale (on a tie, in that order,
// as the timeline sorts them). Past milestones and the ones the plan never
// reaches are skipped; the plan running out is not one, the lasts tile
// already says it.
func (s *Server) nextMilestone(lang i18n.Lang, in engine.Input, res engine.Result) (label string, month domain.Month, ok bool) {
	consider := func(l string, m domain.Month) {
		if m.Before(in.Start) {
			return
		}
		if !ok || m.Index() < month.Index() {
			label, month, ok = l, m, true
		}
	}
	for _, h := range res.Milestones {
		if h.Reached() {
			consider(h.Milestone.Name, h.Month)
		}
	}
	for _, d := range res.PaidOff {
		consider(s.catalog.T(lang, "plan.event.paid_off", d.Debt.Name), d.Month)
	}
	for _, sale := range res.Sales {
		consider(s.catalog.T(lang, "plan.event.sold", sale.Account.Name), sale.Month)
	}
	return label, month, ok
}

// buildGlance returns nil when the plan has nobody in it (the demo stays).
func (s *Server) buildGlance(ctx context.Context, fc finCtx) (*glanceView, error) {
	in, err := s.loadInput(ctx, fc)
	if err != nil || len(in.People) == 0 {
		return nil, err
	}
	res := engine.Simulate(in)
	if len(res.Months) == 0 {
		return nil, nil
	}
	plan, err := s.store.GetPlan(ctx, fc.plan)
	if err != nil {
		return nil, err
	}
	sum, err := s.loadSummary(ctx, fc)
	if err != nil {
		return nil, err
	}

	main := in.People[0]
	g := &glanceView{PlanName: plan.Name, NetWorth: sum.NetWorth, Failed: res.Failed()}
	if g.Failed {
		g.Age = main.AgeAt(res.FailMonth)
	} else {
		g.Age = main.AgeAt(res.Months[len(res.Months)-1].Month)
	}
	g.NextLabel, g.NextMonth, g.HasNext = s.nextMilestone(fc.lang, in, res)

	// The same simulation and cache key as /success with its default seed.
	g.Odds = glanceOdds{Pending: true}
	if mc, ok := cachedSim[engine.MCResult](s.sims, mcKey(in, mcSettings(in, defaultSeed))); ok && mc.Runs > 0 {
		countSimCache(ctx, "monte_carlo", "home", servedPage, true)
		g.Odds = glanceOdds{Rate: mc.SuccessRate()}
	}
	g.Chip = glanceChip(g.Failed, g.Odds.Pending)
	return g, nil
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	g, err := s.buildGlance(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, http.StatusOK, "home", "home.title", homeData{demoData: compoundDemo(fc.lang, s.catalog), Glance: g})
}

// GET /glance/odds: the success-rate tile (and the card's chip, out of band),
// loaded by its placeholder. It uses the same Monte Carlo and cache as /success.
func (s *Server) handleGlanceOdds(w http.ResponseWriter, r *http.Request) {
	fc, err := s.finCtx(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	in, err := s.loadInput(r.Context(), fc)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(in.People) == 0 {
		s.handleNotFound(w, r)
		return
	}
	mc, err := s.monteCarlo(r.Context(), "home", in, mcSettings(in, defaultSeed))
	if err != nil {
		s.simFailed(w, r, err)
		return
	}
	failed := engine.Simulate(in).Failed()
	odds := glanceOdds{Rate: mc.SuccessRate(), Chip: glanceChip(failed, false), OOB: true}
	if err := s.render.render(w, r, http.StatusOK, "home", "glance-odds-result", odds); err != nil {
		s.serverError(w, r, err)
	}
}
