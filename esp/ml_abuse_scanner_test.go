package main

import "testing"

// Phase 60: real automated tests for ScanContent -- it's a pure,
// deterministic function (no DB, no RNG as of Phase 55), so this is exactly
// the kind of logic that should never regress silently. Run with `go test`.

func TestScanContent_CleanBusinessEmailScoresLow(t *testing.T) {
	score := (&MLAbuseScanner{}).ScanContent("org1", "Quarterly report", "Hi team, attached is the Q3 report. Let me know your thoughts.")
	if score > 0.2 {
		t.Errorf("expected a low score for ordinary business email, got %f", score)
	}
}

func TestScanContent_PhishingLanguageScoresHigh(t *testing.T) {
	score := (&MLAbuseScanner{}).ScanContent("org1", "Urgent: Verify your wallet",
		"Your bitcoin wallet address has unusual activity. Click here to verify your account immediately or it will be suspended.")
	if score < 0.8 {
		t.Errorf("expected a high score for stacked phishing indicators, got %f", score)
	}
}

func TestScanContent_IsDeterministic(t *testing.T) {
	scanner := &MLAbuseScanner{}
	subject := "Final Notice"
	body := "URGENT WIRE TRANSFER required via Western Union. Act now."
	first := scanner.ScanContent("org1", subject, body)
	for i := 0; i < 10; i++ {
		got := scanner.ScanContent("org1", subject, body)
		if got != first {
			t.Fatalf("ScanContent is not deterministic: run 1 = %f, run %d = %f", first, i+2, got)
		}
	}
}

func TestScanContent_LegitimacySignalsLowerScore(t *testing.T) {
	scanner := &MLAbuseScanner{}
	withSignal := scanner.ScanContent("org1", "Your invoice #4521", "Order confirmation attached. Unsubscribe anytime.")
	if withSignal > 0.1 {
		t.Errorf("expected legitimacy signals (unsubscribe, order confirmation) to keep score low, got %f", withSignal)
	}
}

func TestScanContent_ScoreAlwaysInRange(t *testing.T) {
	scanner := &MLAbuseScanner{}
	cases := []struct{ subject, body string }{
		{"", ""},
		{"bitcoin bitcoin bitcoin urgent wire lottery inheritance next of kin", "crypto crypto crypto western union act now"},
		{"normal", "normal email with nothing suspicious at all"},
	}
	for _, c := range cases {
		score := scanner.ScanContent("org1", c.subject, c.body)
		if score < 0 || score > 1 {
			t.Errorf("score %f out of [0,1] range for subject=%q", score, c.subject)
		}
	}
}
