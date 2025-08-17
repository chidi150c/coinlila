// cmd/bot/main.go
package main

import (
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/chidi150c/coinlila/internal/config"
	"github.com/chidi150c/coinlila/internal/exchange"
	"github.com/chidi150c/coinlila/internal/guards"
	"github.com/chidi150c/coinlila/internal/metrics"
	"github.com/chidi150c/coinlila/internal/risk"
	"github.com/chidi150c/coinlila/internal/strategy"
	"github.com/chidi150c/coinlila/internal/util"
)

func main() {
	// 0) env + config
	_ = godotenv.Load(".env")
	cfg := config.Load()

	// 1) metrics http server
	metrics.Serve(cfg.HTTPListen)
	log.Printf("coinbot starting | mode=%s symbol=%s listen=%s", cfg.Mode, cfg.Symbol, cfg.HTTPListen)

	// 2) exchange: paper first (recommended) or live coinbase
	var ex exchange.Exchange
	priceCh := make(chan exchange.Ticker, 256)

	if cfg.Mode == "paper" {
		paper := exchange.NewPaper(usdStart())
		ex = paper

		// use coinbase WS as price feed only
		cb := exchange.NewCoinbase(cfg.CBAPIKey, cfg.CBAPISecret, cfg.CBAPIPassphrase, cfg.CBAPIBase, cfg.CBWSURL)
		stopWS, err := cb.StreamPrices(cfg.Symbol, priceCh)
		if err != nil { log.Fatalf("ws connect (paper feed): %v", err) }
		defer stopWS()

		// pipe live prices into the paper engine
		go func() {
			for t := range priceCh {
				paper.UpdatePrice(t.Symbol, t.Price) // ensure Paper has an exported UpdatePrice
			}
		}()
	} else {
		cb := exchange.NewCoinbase(cfg.CBAPIKey, cfg.CBAPISecret, cfg.CBAPIPassphrase, cfg.CBAPIBase, cfg.CBWSURL)
		ex = cb
		if _, err := cb.StreamPrices(cfg.Symbol, priceCh); err != nil {
			log.Fatalf("ws connect (live): %v", err)
		}
	}

	// 3) account & day boundary state
	acct, err := ex.Account()
	if err != nil { log.Fatalf("account read failed: %v", err) }

	now := time.Now()
	tz := getenv("RISK_TIMEZONE", "UTC")
	dayMgr := risk.NewDayManager(tz, "day_snapshot.json")
	rs := risk.NewState(acct.EquityUSD, mustInt("ERROR_COOLDOWN_SEC"), util.TodayOpen(tz, now))
	_, equityOpen := dayMgr.InitAtStartup(now, acct.EquityUSD, rs)
	log.Printf("equity_open=%.2f", equityOpen)

	// 4) limits + safe wrapper (rate-limit, retries, dup, breaker)
	lim := risk.Limits{
		MaxPositionUSD:      mustF("MAX_POSITION_USD"),
		MaxOrderNotionalUSD: mustF("MAX_ORDER_NOTIONAL_USD"),
		MaxOrdersPerDay:     mustInt("MAX_ORDERS_PER_DAY"),
		MaxLossPctDay:       mustF("MAX_LOSS_PCT_DAY"),
		VolSizingOn:         getenv("VOL_SIZING_ON", "false") == "true",
		VolLookback:         mustInt("VOL_LOOKBACK"),
		TargetRiskBp:        mustF("TARGET_RISK_BP"),
	}

	perMin := mustInt("RATE_LIMIT_ORDERS_PER_MIN")
	retries := mustInt("MAX_ORDER_RETRIES")
	backoff := time.Duration(mustInt("RETRY_BACKOFF_MS")) * time.Millisecond
	dupWin := time.Duration(mustInt("DUP_SUPPRESS_WINDOW_MS")) * time.Millisecond
	brThresh := mustInt("BREAKER_THRESHOLD")
	brCooldown := time.Duration(mustInt("BREAKER_COOLDOWN_SEC")) * time.Second
	brProbes := mustInt("BREAKER_HALFOPEN_PROBES")

	safeEx := guards.NewSafeExchange(
		ex, rs, lim,
		perMin, retries, backoff,
		dupWin, brThresh, brCooldown, brProbes,
	)

	// 5) strategy (SMA as simple baseline)
	sma := strategy.NewSMA(cfg.SMAFast, cfg.SMASlow)

	// 6) loop + shutdown
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-quit:
			log.Println("shutting down")
			return

		case <-tick.C:
			now = time.Now()

			// price (from exchange BBA; WS feeds exchange impl)
			bid, ask, err := safeEx.BestBidAsk(cfg.Symbol)
			if err != nil || bid <= 0 || ask <= 0 {
				continue // wait until we have a price
			}
			price := (bid + ask) / 2

			// risk: vol window + equity
			if lim.VolLookback > 0 { rs.PushPrice(price, lim.VolLookback) }
			if acct, err = safeEx.Account(); err == nil {
				rs.UpdateEquity(acct.EquityUSD)
			}

			// day boundary (persist & reset when needed)
			if dayMgr.RolloverIfNeeded(now, rs.EquityNowUSD, rs) {
				// optional: send alert here
			}
			dayMgr.PersistProgress(now, rs)

			// strategy signal
			have, fast, slow, cross := sma.Push(price)
			if !have { continue }

			// current exposure (best-effort from Account())
			posUSD, posQty := currentExposureForSymbol(acct, cfg.Symbol, price)

			switch cross {
			case "golden": // try to buy
				dec := risk.DecideBuy(rs, lim, price, posUSD)
				if dec.Allow {
					if _, err := safeEx.PlaceMarket(cfg.Symbol, exchange.Buy, dec.Qty); err != nil {
						log.Printf("BUY blocked: %v", err)
					} else {
						log.Printf("BUY %.8f @ %.2f | fast=%.2f slow=%.2f | notional=%.2f",
							dec.Qty, price, fast, slow, dec.NotionalUSD)
					}
				} else {
					log.Printf("BUY denied: %s", dec.Reason)
				}

			case "death": // try to sell (size-limited)
				dec := risk.DecideSell(rs, lim, price, posQty)
				if dec.Allow {
					if _, err := safeEx.PlaceMarket(cfg.Symbol, exchange.Sell, dec.Qty); err != nil {
						log.Printf("SELL blocked: %v", err)
					} else {
						log.Printf("SELL %.8f @ %.2f | fast=%.2f slow=%.2f | notional=%.2f",
							dec.Qty, price, fast, slow, dec.NotionalUSD)
					}
				} else {
					log.Printf("SELL denied: %s", dec.Reason)
				}
			default:
				// flat
			}
		}
	}
}

// ----- helpers -----

func usdStart() float64 { return 1000.0 }

func mustInt(k string) int {
	v, _ := strconv.Atoi(os.Getenv(k))
	return v
}
func mustF(k string) float64 {
	v, _ := strconv.ParseFloat(os.Getenv(k), 64)
	return v
}
func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" { return v }
	return def
}

func currentExposureForSymbol(ac exchange.Account, symbol string, price float64) (posUSD, posQty float64) {
	if ac.Positions == nil { return 0, 0 }
	if pos, ok := ac.Positions[symbol]; ok {
		posQty = pos.BaseQty
		posUSD = posQty * price
		return posUSD, posQty
	}
	return 0, 0
}
