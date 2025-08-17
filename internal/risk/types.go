package risk

import "time"

// Limits defines static configuration for risk controls.
type Limits struct {
	MaxPositionUSD       float64 // maximum exposure in USD
	MaxOrderNotionalUSD  float64 // max USD size per single order
	MaxOrdersPerDay      int     // order count cap per day
	MaxLossPctDay        float64 // daily kill-switch loss threshold (%)

	VolSizingOn          bool    // enable volatility-aware sizing
	VolLookback          int     // number of ticks for realized vol
	TargetRiskBp         float64 // target basis points risk per trade (e.g., 50 = 0.50%)
}

// State tracks dynamic trading state and rolling metrics.
type State struct {
	EquityAtOpenUSD   float64   // starting equity at day open
	EquityNowUSD      float64   // updated equity
	OrdersToday       int       // count of orders sent
	RealizedPnLUSD    float64   // realized PnL tracker

	LastErrorTime     time.Time // for cooldowns
	ErrorCooldown     time.Duration
	DayOpen           time.Time // anchored day open (UTC or configured TZ)

	prices            []float64 // rolling window of prices for realized vol
}

// Decision is returned when evaluating a trade against limits.
type Decision struct {
	Allow       bool    // true if trade allowed
	Reason      string  // denial reason
	NotionalUSD float64 // suggested notional size in USD
	Qty         float64 // suggested asset quantity
}
