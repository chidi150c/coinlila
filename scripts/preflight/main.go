package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

func fail(msg string) { log.Fatalf("FAIL: %s", msg) }
func pass(msg string) { fmt.Println("PASS:", msg) }

func main() {
	// Load .env (do not overwrite existing env)
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(".env"); err != nil { fail("cannot load .env") }
	} else { fail(".env missing") }

	mode := os.Getenv("MODE")
	if mode == "" { fail("MODE missing") }
	if mode != "paper" { fail("MODE must be 'paper' at Phase 0") }
	pass("MODE is paper")

	symbol := os.Getenv("SYMBOL")
	if symbol == "" || !strings.Contains(symbol, "-") { fail("SYMBOL missing or not like BASE-QUOTE (e.g., BTC-USD)") }
	pass("SYMBOL looks OK: " + symbol)

	// Coinbase endpoints present (not verifying correctness yet)
	if os.Getenv("COINBASE_API_BASE") == "" || os.Getenv("COINBASE_WS_URL") == "" {
		fail("COINBASE_API_BASE or COINBASE_WS_URL missing")
	}
	pass("Exchange endpoints present")

	// Risk knobs present
	for _, k := range []string{"MAX_POSITION_USD","MAX_LOSS_PCT_DAY","MIN_TRADE_USD"} {
		if os.Getenv(k) == "" { fail(k + " missing") }
	}
	pass("Risk knobs present")

	// Warn if any live-ish hints
	key := os.Getenv("COINBASE_API_KEY")
	sec := os.Getenv("COINBASE_API_SECRET")
	if key != "" || sec != "" {
		fmt.Println("NOTE: API key/secret present in .env (still OK for paper). Ensure they are PAPER/SANDBOX keys only and .env is gitignored.")
	} else {
		pass("No API keys set yet (safest for Phase 0)")
	}

	pass("Preflight completed")
}
