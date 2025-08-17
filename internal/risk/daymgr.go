package risk

import (
	"log"
	"time"

	"example.com/coinbot/internal/util"
)

type DayManager struct {
	TZ          string
	Path        string // snapshot file path
}

func NewDayManager(tz, path string) *DayManager {
	if tz == "" { tz = "UTC" }
	return &DayManager{TZ: tz, Path: path}
}

// InitAtStartup loads or seeds today's snapshot and initializes the risk state.
// Returns the loaded snapshot and the chosen EquityAtOpen.
func (dm *DayManager) InitAtStartup(now time.Time, equityNow float64, rs *State) (util.DaySnapshot, float64) {
	// default: seed from now
	seed := util.SeedForToday(dm.TZ, now, equityNow)

	snap, err := util.LoadSnapshot(dm.Path)
	if err != nil {
		_ = util.SaveSnapshot(dm.Path, seed)
		rs.ResetDay(seed.EquityAtOpenUSD, util.TodayOpen(dm.TZ, now))
		log.Printf("[daymgr] seeded snapshot for today (tz=%s)", dm.TZ)
		return seed, seed.EquityAtOpenUSD
	}

	dayOpenPrev, err := util.ParseDayOpenISO(snap.DayOpenISO)
	if err != nil || !util.SameTradingDay(dm.TZ, dayOpenPrev, now) {
		// Old snapshot → start a fresh trading day
		snap = seed
		_ = util.SaveSnapshot(dm.Path, snap)
		rs.ResetDay(snap.EquityAtOpenUSD, util.TodayOpen(dm.TZ, now))
		log.Printf("[daymgr] rolled snapshot to today (tz=%s)", dm.TZ)
		return snap, snap.EquityAtOpenUSD
	}

	// Same day: reuse
	rs.ResetDay(snap.EquityAtOpenUSD, util.TodayOpen(dm.TZ, now))
	rs.OrdersToday = snap.OrdersToday
	rs.RealizedPnLUSD = snap.RealizedPnLUSD
	log.Printf("[daymgr] loaded snapshot for today (tz=%s)", dm.TZ)
	return snap, snap.EquityAtOpenUSD
}

// RolloverIfNeeded checks the boundary; when hit, it writes a fresh snapshot with `equityNow` as the new EquityAtOpenUSD,
// resets the risk state, and returns true. Call this once per tick.
func (dm *DayManager) RolloverIfNeeded(now time.Time, equityNow float64, rs *State) bool {
	if util.SameTradingDay(dm.TZ, rs.DayOpen, now) {
		return false
	}
	// Crossed into a new trading day
	newSnap := util.SeedForToday(dm.TZ, now, equityNow)
	if err := util.SaveSnapshot(dm.Path, newSnap); err != nil {
		log.Printf("[daymgr] ERROR saving new snapshot: %v", err)
	}
	rs.ResetDay(equityNow, util.TodayOpen(dm.TZ, now))
	log.Printf("[daymgr] new trading day started; equity_open=%.2f", equityNow)
	return true
}

// PersistProgress can be called periodically to keep OrdersToday/RealizedPnL durable.
func (dm *DayManager) PersistProgress(now time.Time, rs *State) {
	snap := util.DaySnapshot{
		DayOpenISO:      util.TodayOpen(dm.TZ, now).UTC().Format(time.RFC3339),
		Timezone:        dm.TZ,
		EquityAtOpenUSD: rs.EquityAtOpenUSD,
		OrdersToday:     rs.OrdersToday,
		RealizedPnLUSD:  rs.RealizedPnLUSD,
	}
	_ = util.SaveSnapshot(dm.Path, snap) // best-effort
}
