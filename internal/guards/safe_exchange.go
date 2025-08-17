package guards

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"example.com/coinbot/internal/exchange"
	"example.com/coinbot/internal/risk"
)

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerHalfOpen
	breakerOpen
)

var (
	metricOrdersAttempted  = prometheus.NewCounter(prometheus.CounterOpts{Name: "bot_orders_attempted_total", Help: "Orders the bot tried to place"})
	metricOrdersPlaced     = prometheus.NewCounter(prometheus.CounterOpts{Name: "bot_orders_placed_total", Help: "Orders successfully handed to exchange"})
	metricOrdersFailed     = prometheus.NewCounter(prometheus.CounterOpts{Name: "bot_orders_failed_total", Help: "Orders that failed after retries"})
	metricOrdersSuppressed = prometheus.NewCounter(prometheus.CounterOpts{Name: "bot_orders_suppressed_total", Help: "Orders blocked by safety layer (rate/idempotency/breaker/cooldown)"})
	metricBreakerState     = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bot_breaker_state", Help: "0=closed, 1=half_open, 2=open"})
	metricRateWindow       = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bot_orders_in_last_minute", Help: "Orders counted in the current minute window"})
)

func init() {
	prometheus.MustRegister(
		metricOrdersAttempted, metricOrdersPlaced, metricOrdersFailed,
		metricOrdersSuppressed, metricBreakerState, metricRateWindow,
	)
	metricBreakerState.Set(0)
}

// SafeExchange wraps an exchange with rate limits, retries, circuit breaker, and duplicate suppression.
type SafeExchange struct {
	inner exchange.Exchange
	riskS *risk.State
	lim   risk.Limits

	// Rate limiting (simple sliding window)
	rateMu       sync.Mutex
	orderTimes   []time.Time
	perMinuteCap int

	// Retries
	maxRetries int
	backoff    time.Duration

	// Duplicate suppression
	dupWindow    time.Duration
	lastOrderKey string
	lastOrderAt  time.Time

	// Circuit breaker
	bMu        sync.Mutex
	bState     breakerState
	failStreak int
	threshold  int
	cooldown   time.Duration
	openedAt   time.Time
	halfProbes int
	halfMax    int
}

func NewSafeExchange(
	inner exchange.Exchange,
	rs *risk.State,
	lim risk.Limits,
	perMinuteCap int,
	maxRetries int,
	backoff time.Duration,
	dupWindow time.Duration,
	breakerThreshold int,
	breakerCooldown time.Duration,
	breakerHalfOpenProbes int,
) *SafeExchange {
	if breakerThreshold < 1 { breakerThreshold = 3 }
	if breakerHalfOpenProbes < 1 { breakerHalfOpenProbes = 1 }
	se := &SafeExchange{
		inner:        inner,
		riskS:        rs,
		lim:          lim,
		perMinuteCap: perMinuteCap,
		maxRetries:   maxRetries,
		backoff:      backoff,
		dupWindow:    dupWindow,
		bState:       breakerClosed,
		threshold:    breakerThreshold,
		cooldown:     breakerCooldown,
		halfMax:      breakerHalfOpenProbes,
	}
	return se
}

func (s *SafeExchange) BestBidAsk(symbol string) (float64, float64, error) { return s.inner.BestBidAsk(symbol) }
func (s *SafeExchange) Account() (exchange.Account, error)                  { return s.inner.Account() }
func (s *SafeExchange) StreamPrices(symbol string, out chan<- exchange.Ticker) (func(), error) {
	return s.inner.StreamPrices(symbol, out)
}

func (s *SafeExchange) PlaceMarket(symbol string, side exchange.Side, qty float64) (exchange.Order, error) {
	now := time.Now()
	metricOrdersAttempted.Inc()

	// Cooldown after previous error
	if !s.riskS.CanAct(now) {
		metricOrdersSuppressed.Inc()
		return exchange.Order{}, errors.New("cooldown active after error")
	}

	// Circuit breaker gating
	if !s.allowBreaker(now) {
		metricOrdersSuppressed.Inc()
		return exchange.Order{}, errors.New("circuit breaker open/half-open blocking")
	}

	// Per-minute rate limit
	if s.rateExceeded(now) {
		metricOrdersSuppressed.Inc()
		return exchange.Order{}, errors.New("rate limit hit")
	}

	// Duplicate suppression (idempotency window)
	okey := s.ordKey(symbol, side, qty)
	if okey == s.lastOrderKey && now.Sub(s.lastOrderAt) < s.dupWindow {
		metricOrdersSuppressed.Inc()
		return exchange.Order{}, errors.New("duplicate order suppressed")
	}

	// Try with retries + backoff
	var ord exchange.Order
	var err error
	for i := 0; i <= s.maxRetries; i++ {
		ord, err = s.inner.PlaceMarket(symbol, side, qty)
		if err == nil {
			s.noteSuccess(now, okey)
			return ord, nil
		}
		time.Sleep(time.Duration(i+1) * s.backoff)
	}
	// Final failure
	s.noteFailure(now)
	metricOrdersFailed.Inc()
	return ord, err
}

// ===== Helpers =====

func (s *SafeExchange) ordKey(symbol string, side exchange.Side, qty float64) string {
	h := sha256.Sum256([]byte(symbol + string(side) + strconv.FormatFloat(qty, 'f', 8, 64)))
	return hex.EncodeToString(h[:8])
}

func (s *SafeExchange) rateExceeded(now time.Time) bool {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	oneMin := now.Add(-1 * time.Minute)
	// keep only recent timestamps
	j := 0
	for _, t := range s.orderTimes {
		if t.After(oneMin) {
			s.orderTimes[j] = t
			j++
		}
	}
	s.orderTimes = s.orderTimes[:j]
	metricRateWindow.Set(float64(len(s.orderTimes)))
	if s.perMinuteCap > 0 && len(s.orderTimes) >= s.perMinuteCap {
		return true
	}
	return false
}

func (s *SafeExchange) rateNote(t time.Time) {
	s.rateMu.Lock()
	s.orderTimes = append(s.orderTimes, t)
	metricRateWindow.Set(float64(len(s.orderTimes)))
	s.rateMu.Unlock()
}

func (s *SafeExchange) allowBreaker(now time.Time) bool {
	s.bMu.Lock()
	defer s.bMu.Unlock()

	switch s.bState {
	case breakerClosed:
		return true
	case breakerOpen:
		// move to half-open after cooldown
		if now.Sub(s.openedAt) >= s.cooldown {
			s.bState = breakerHalfOpen
			s.halfProbes = 0
			metricBreakerState.Set(1)
			return true // allow a probe
		}
		return false
	case breakerHalfOpen:
		// allow limited probes
		if s.halfProbes < s.halfMax {
			return true
		}
		// no more probes; keep waiting (should not really happen unless caller loops too fast)
		return false
	default:
		return false
	}
}

func (s *SafeExchange) noteSuccess(now time.Time, okey string) {
	// update rate and dup keys
	s.rateNote(now)
	s.lastOrderKey, s.lastOrderAt = okey, now
	metricOrdersPlaced.Inc()

	// breaker transitions
	s.bMu.Lock()
	defer s.bMu.Unlock()
	switch s.bState {
	case breakerClosed:
		s.failStreak = 0
	case breakerHalfOpen:
		// success in half-open -> close
		s.bState = breakerClosed
		s.failStreak = 0
		metricBreakerState.Set(0)
	case breakerOpen:
		// shouldn't get here (allowBreaker would have blocked), ignore
	}
}

func (s *SafeExchange) noteFailure(now time.Time) {
	s.riskS.NoteError()

	s.bMu.Lock()
	defer s.bMu.Unlock()

	switch s.bState {
	case breakerClosed:
		s.failStreak++
		if s.failStreak >= s.threshold {
			s.openedAt = now
			s.bState = breakerOpen
			metricBreakerState.Set(2)
		}
	case breakerHalfOpen:
		// failed probe -> reopen immediately
		s.openedAt = now
		s.bState = breakerOpen
		s.failStreak = s.threshold // keep at threshold
		metricBreakerState.Set(2)
	case breakerOpen:
		// already open, keep timer fresh (optional)
		s.openedAt = now
	}
	metricOrdersSuppressed.Inc()
}
