package connector

import "strings"

// knownCurrencies is a deliberately small, explicit ISO 4217 allow-
// list, not a claim of completeness. Every source package in this
// directory that validates a currency code (payment, insurance) uses
// this ONE list rather than five independent copies, so extending it
// is a one-line, one-place change instead of a silent divergence.
var knownCurrencies = map[string]bool{
	"USD": true, "EUR": true, "GBP": true, "JPY": true, "CNY": true,
	"SGD": true, "HKD": true, "AUD": true, "CAD": true, "CHF": true,
	"AED": true, "INR": true, "KRW": true, "NOK": true, "SEK": true,
}

// KnownCurrency reports whether code (case-insensitive) is on this
// package's ISO 4217 allow-list. A currency this repo has never seen
// declared is a "fail closed, don't guess" situation for a financial
// evidence contract, not a "shrug and accept it anyway" one.
func KnownCurrency(code string) bool {
	return knownCurrencies[strings.ToUpper(strings.TrimSpace(code))]
}
