package risk

import (
	"math"
	"time"
)

// NewState initializes risk state at day open with equity snapshot.
func NewState(equityAtOpenUSD float64, cooldownSec int, dayOpen time.Time) *State {
	return &State{
		EquityAtOpenUSD: equityAtOpenUSD,
		ErrorCooldown:   time.Duration(cooldownSec) * time.Second,
		DayOpen:         dayOpen,
	}
}

// Error handling & cooldowns
func (s *State) NoteError()                { s.LastErrorTime = time.Now() }
func (s *State) CanAct(now time.Time) bool { return time.Since(s.LastErrorTime) >= s.ErrorCooldown }

// Daily reset
func (s *State) ResetDay(newEquity float64, newOpen time.Time) {
	s.EquityAtOpenUSD = newEquity
	s.EquityNowUSD = newEquity
	s.OrdersToday = 0
	s.RealizedPnLUSD = 0
	s.DayOpen = newOpen
	s.prices = s.prices[:0]
}

// Equity update
func (s *State) UpdateEquity(current float64) { s.EquityNowUSD = current }

// Kill-switch check
func (s *State) BreachDailyLoss(maxLossPct float64) bool {
	if s.EquityAtOpenUSD <= 0 {
		return false
	}
	lossPct := (s.EquityAtOpenUSD - s.EquityNowUSD) / s.EquityAtOpenUSD * 100
	return lossPct >= maxLossPct
}

// Order counter
func (s *State) CountOrder() { s.OrdersToday++ }

// --- Volatility helpers ---
func (s *State) PushPrice(px float64, lookback int) {
	s.prices = append(s.prices, px)
	if len(s.prices) > lookback {
		s.prices = s.prices[1:]
	}
}

// Simple realized volatility estimator
func (s *State) RealizedVol() float64 {
	n := len(s.prices)
	if n < 2 {
		return 0
	}
	// compute simple returns
	var rets []float64
	for i := 1; i < n; i++ {
		if s.prices[i-1] == 0 {
			continue
		}
		r := (s.prices[i] - s.prices[i-1]) / s.prices[i-1]
		rets = append(rets, r)
	}
	if len(rets) == 0 {
		return 0
	}
	// mean
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	// variance
	var varsum float64
	for _, r := range rets {
		d := r - mean
		varsum += d * d
	}
	return math.Sqrt(varsum / float64(len(rets))) // standard deviation of returns
}
