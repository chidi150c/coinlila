package util

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

type DaySnapshot struct {
	// Trading day anchor
	DayOpenISO       string  `json:"day_open_iso"`
	Timezone         string  `json:"timezone"`

	// Equity at start-of-day (used for daily loss %)
	EquityAtOpenUSD  float64 `json:"equity_at_open_usd"`

	// Optional helpful counters (persisted across restarts)
	OrdersToday      int     `json:"orders_today"`
	RealizedPnLUSD   float64 `json:"realized_pnl_usd"`
}

func LoadSnapshot(path string) (DaySnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil { return DaySnapshot{}, err }
	var s DaySnapshot
	if err := json.Unmarshal(b, &s); err != nil { return DaySnapshot{}, err }
	return s, nil
}

func SaveSnapshot(path string, s DaySnapshot) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil { return err }
	// best-effort .bak
	_ = os.WriteFile(path+".bak", b, 0o600)
	return writeFileAtomic(path, b, 0o600)
}

// SeedForToday builds a snapshot for the current trading day.
func SeedForToday(tz string, now time.Time, equityAtOpen float64) DaySnapshot {
	return DaySnapshot{
		DayOpenISO:      TodayOpen(tz, now).UTC().Format(time.RFC3339),
		Timezone:        tz,
		EquityAtOpenUSD: equityAtOpen,
		OrdersToday:     0,
		RealizedPnLUSD:  0,
	}
}

func ParseDayOpenISO(s string) (time.Time, error) {
	if s == "" { return time.Time{}, errors.New("empty day_open_iso") }
	t, err := time.Parse(time.RFC3339, s)
	if err != nil { return time.Time{}, err }
	return t
}
