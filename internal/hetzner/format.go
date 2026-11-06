package hetzner

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	// HourlyFrac is the decimals an hourly price shows: Hetzner's rates need
	// four to differ from each other.
	HourlyFrac = 4
	// MonthlyFrac is the decimals a monthly price shows, like the invoice.
	MonthlyFrac = 2
)

// FormatMoney renders an API price with the currency's symbol and frac
// decimals, or a dash when there is no price.
func FormatMoney(currency, amount string, frac int) string {
	text, ok := FormatAmount(amount, frac)
	if !ok {
		return "—"
	}

	return currencySymbol(currency) + text
}

// FormatAmount renders an API price with frac decimals and no symbol. The
// second result is false when there is no price.
func FormatAmount(amount string, frac int) (string, bool) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return "", false
	}

	value, err := strconv.ParseFloat(amount, 64)
	if err != nil {
		return "", false
	}

	return strconv.FormatFloat(value, 'f', frac, 64), true
}

func currencySymbol(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "", "EUR":
		return "€"
	case "USD":
		return "$"
	case "GBP":
		return "£"
	default:
		return strings.ToUpper(code) + " "
	}
}

// MonthlyValue parses an API price for sorting. A missing or unreadable
// price sorts last.
func MonthlyValue(amount string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(amount), 64)
	if err != nil {
		return math.MaxFloat64
	}

	return value
}

// MemoryLabel prints memory in GB, with a decimal only when Hetzner lists
// a fraction.
func MemoryLabel(gb float32) string {
	if gb == float32(int(gb)) {
		return fmt.Sprintf("%d GB", int(gb))
	}

	return fmt.Sprintf("%.1f GB", gb)
}
