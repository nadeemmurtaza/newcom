package main

import "testing"

// Phase 60: real tests for the CEL policy evaluator (cel_eval.go), which
// replaced a hand-parsed, single-hardcoded-expression-shape "simulated CEL
// Engine" in Phase 54. These exercise the actual compiled expression path,
// not a mock.

const testCELExpression = `device.is_managed && device.screen_lock_enabled || in_subnet(request.ip, "198.51.100.0/24")`

func TestEvaluateCELPolicy_ManagedDeviceWithScreenLockPasses(t *testing.T) {
	ctx := RequestContext{IP: "203.0.113.5", IsManaged: true, ScreenLockEnabled: true}
	ok, err := evaluateCELPolicy(testCELExpression, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected managed+screenlocked device to pass")
	}
}

func TestEvaluateCELPolicy_UnmanagedDeviceOutsideSubnetFails(t *testing.T) {
	ctx := RequestContext{IP: "203.0.113.5", IsManaged: false, ScreenLockEnabled: false}
	ok, err := evaluateCELPolicy(testCELExpression, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected unmanaged device outside the allowed subnet to fail")
	}
}

func TestEvaluateCELPolicy_InAllowedSubnetPassesEvenUnmanaged(t *testing.T) {
	ctx := RequestContext{IP: "198.51.100.42", IsManaged: false, ScreenLockEnabled: false}
	ok, err := evaluateCELPolicy(testCELExpression, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("expected an IP inside 198.51.100.0/24 to pass via in_subnet(), regardless of device state")
	}
}

func TestEvaluateCELPolicy_ManagedButNoScreenLockFailsOutsideSubnet(t *testing.T) {
	ctx := RequestContext{IP: "203.0.113.5", IsManaged: true, ScreenLockEnabled: false}
	ok, err := evaluateCELPolicy(testCELExpression, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected managed-but-unlocked device outside the subnet to fail (both device conditions required)")
	}
}

func TestEvaluateCELPolicy_InvalidExpressionErrorsRatherThanPassing(t *testing.T) {
	// Phase 54's whole point was to stop silently falling through to a
	// permissive default on anything the hand-parser didn't recognize --
	// an expression that doesn't even compile must error, not pass.
	ctx := RequestContext{IP: "203.0.113.5", IsManaged: true, ScreenLockEnabled: true}
	ok, err := evaluateCELPolicy("this is not valid CEL +++ ", ctx)
	if err == nil {
		t.Fatalf("expected a compile error for invalid CEL, got none (ok=%v)", ok)
	}
	if ok {
		t.Errorf("expected ok=false alongside the error")
	}
}

func TestEvaluateCELPolicy_NonBooleanExpressionErrors(t *testing.T) {
	ctx := RequestContext{IP: "203.0.113.5", IsManaged: true, ScreenLockEnabled: true}
	_, err := evaluateCELPolicy(`"not a boolean"`, ctx)
	if err == nil {
		t.Fatalf("expected an error for a non-boolean CEL result")
	}
}

func TestCelInSubnet_RealCIDRMatching(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{`in_subnet("198.51.100.1", "198.51.100.0/24")`, true},
		{`in_subnet("198.51.100.255", "198.51.100.0/24")`, true},
		{`in_subnet("198.51.101.1", "198.51.100.0/24")`, false},
		{`in_subnet("not-an-ip", "198.51.100.0/24")`, false},
		{`in_subnet("198.51.100.1", "not-a-cidr")`, false},
	}
	for _, c := range cases {
		ok, err := evaluateCELPolicy(c.expr, RequestContext{})
		if err != nil {
			t.Fatalf("expr %q: unexpected error: %v", c.expr, err)
		}
		if ok != c.want {
			t.Errorf("expr %q = %v, want %v", c.expr, ok, c.want)
		}
	}
}
