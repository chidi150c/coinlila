package util

import "time"

// TodayOpen returns the local midnight (00:00) for `now` in tz.
func TodayOpen(tz string, now time.Time) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil { loc = time.UTC }
	y, m, d := now.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// NextOpen returns the next local midnight after `now` in tz.
func NextOpen(tz string, now time.Time) time.Time {
	o := TodayOpen(tz, now)
	return o.Add(24 * time.Hour)
}

// SameTradingDay checks if a and b are on the same local day in tz.
func SameTradingDay(tz string, a, b time.Time) bool {
	return TodayOpen(tz, a).Equal(TodayOpen(tz, b))
}
