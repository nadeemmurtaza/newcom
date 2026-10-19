package main

import (
	"database/sql"
	"math"
	"regexp"
	"strings"
)

// Phase 48/55: Anti-Abuse Content Scoring
//
// This previously claimed to call a "ml-anti-abuse-service" microservice
// (the HTTP call was commented out specifically to dodge a real network
// error) and then faked the result with rand.Float64() -- clean mail and
// phishing mail were scored by coin flip, the keyword check only nudged
// which range the coin flip fell in. This replaces it with a real,
// deterministic calculation: a naive Bayes-style log-odds classifier over
// a curated phishing/spam indicator vocabulary. It is not a trained neural
// network -- there's no training corpus or GPU available here -- but it is
// real statistics, not randomness: the same input always produces the same
// score, and the score is an actual sum of evidence weights run through a
// logistic function, exactly how a trained Naive Bayes/logistic model
// would combine per-token log-likelihood ratios.
//
// Swapping this for a real trained model or a remote inference service
// later is a drop-in replacement: ScanContent's signature doesn't need to
// change, only its body.

// indicatorWeights maps a lowercase phrase to its log-likelihood-ratio
// contribution toward "phishing/abuse" (positive) vs "legitimate" (negative).
// Weights are hand-set from well-documented phishing/BEC/419-scam patterns,
// in the same spirit as the weights a Naive Bayes model would learn from a
// labeled corpus -- and deliberately conservative so ordinary business
// email doesn't trip them without several indicators stacking up.
var indicatorWeights = map[string]float64{
	// Credential/account phishing
	"verify your account":    2.2,
	"verify your wallet":     2.6,
	"confirm your password":  2.4,
	"suspended":              1.1,
	"unusual activity":       1.3,
	"click here to verify":   2.3,
	"update your billing":    1.4,
	"account will be closed": 1.8,

	// Financial / crypto / BEC fraud
	"bitcoin":                2.0,
	"wire transfer":          1.6,
	"gift card":              1.9,
	"wallet address":         1.7,
	"urgent wire":            2.5,
	"western union":          1.8,
	"crypto":                 1.2,

	// Classic 419/lottery scam language
	"lottery":          2.1,
	"inheritance":      1.7,
	"next of kin":      2.0,
	"beneficiary fund": 1.9,

	// Urgency/pressure tactics (weak signal alone, meaningful stacked)
	"urgent action required": 1.5,
	"act now":                1.0,
	"immediately":            0.4,
	"final notice":           1.1,

	// Mild legitimacy signals (negative weight)
	"unsubscribe":      -0.6,
	"privacy policy":   -0.5,
	"order confirmation": -0.8,
	"invoice #":        -0.4,
}

var wordSplitPattern = regexp.MustCompile(`[^a-z0-9 ]+`)

// scanBias is the intercept: with zero matched indicators the score should
// sit comfortably in "clean" territory.
const scanBias = -4.0

type MLAbuseScanner struct {
	db *sql.DB
}

func NewMLAbuseScanner(db *sql.DB) *MLAbuseScanner {
	return &MLAbuseScanner{db: db}
}

// ScanContent returns a deterministic phishing/abuse probability in [0, 1].
func (ml *MLAbuseScanner) ScanContent(orgID, subject, body string) float64 {
	text := wordSplitPattern.ReplaceAllString(strings.ToLower(subject+" "+body), " ")

	logOdds := scanBias
	for phrase, weight := range indicatorWeights {
		if strings.Contains(text, phrase) {
			logOdds += weight
		}
	}

	// Logistic function: converts the accumulated log-odds evidence into a
	// probability. This is the same final step a trained logistic
	// regression or Naive Bayes classifier applies to its own log-odds sum.
	return 1.0 / (1.0 + math.Exp(-logOdds))
}
