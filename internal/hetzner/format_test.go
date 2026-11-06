package hetzner

import (
	"math"
	"testing"
)

func TestFormatMoneyUsesTheSymbolAndFraction(t *testing.T) {
	t.Parallel()

	cases := []struct {
		currency string
		amount   string
		frac     int
		want     string
	}{
		{testCurrency, "4.9900000000", MonthlyFrac, "€4.99"},
		{testCurrency, "0.0080000000", HourlyFrac, "€0.0080"},
		{"USD", "1.5", MonthlyFrac, "$1.50"},
		{"CHF", "2", MonthlyFrac, "CHF 2.00"},
		{testCurrency, "", MonthlyFrac, "—"},
		{testCurrency, "free", MonthlyFrac, "—"},
	}

	for _, testCase := range cases {
		if got := FormatMoney(testCase.currency, testCase.amount, testCase.frac); got != testCase.want {
			t.Fatalf("%+v: got %q", testCase, got)
		}
	}
}

func TestFormatAmountMonthlyValueAndMemoryLabel(t *testing.T) {
	t.Parallel()

	if got, ok := FormatAmount(" 3.7900000000 ", MonthlyFrac); !ok || got != "3.79" {
		t.Fatalf("amount = %q ok=%v", got, ok)
	}

	if _, ok := FormatAmount("", MonthlyFrac); ok {
		t.Fatal("an empty amount was formatted")
	}

	if MonthlyValue("") != math.MaxFloat64 || MonthlyValue("6.49") != 6.49 {
		t.Fatalf("monthly values = %v, %v", MonthlyValue(""), MonthlyValue("6.49"))
	}

	if MemoryLabel(4) != "4 GB" || MemoryLabel(3.5) != "3.5 GB" {
		t.Fatalf("memory = %q, %q", MemoryLabel(4), MemoryLabel(3.5))
	}
}
