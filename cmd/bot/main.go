// after you build `ex` (paper or coinbase) and risk state (rs) + limits (lim)
perMin := mustInt("RATE_LIMIT_ORDERS_PER_MIN")
retries := mustInt("MAX_ORDER_RETRIES")
backoff := time.Duration(mustInt("RETRY_BACKOFF_MS")) * time.Millisecond
dupWin := time.Duration(mustInt("DUP_SUPPRESS_WINDOW_MS")) * time.Millisecond
brThresh := mustInt("BREAKER_THRESHOLD")
brCooldown := time.Duration(mustInt("BREAKER_COOLDOWN_SEC")) * time.Second
brProbes := mustInt("BREAKER_HALFOPEN_PROBES")

safeEx := guards.NewSafeExchange(ex, rs, lim, perMin, retries, backoff, dupWin, brThresh, brCooldown, brProbes)

// use safeEx instead of ex for all order placements and data reads
bid, ask, _ := safeEx.BestBidAsk(cfg.Symbol)
price := (bid + ask) / 2
// ... compute decision via risk.DecideBuy/Sell ...
// example:
if dec.Allow {
    if _, err := safeEx.PlaceMarket(cfg.Symbol, exchange.Buy, dec.Qty); err != nil {
        log.Printf("buy blocked: %v", err)
    }
}





