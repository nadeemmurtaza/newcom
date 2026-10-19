package main

import (
	"fmt"
	"net"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// Phase 54: Real CEL Evaluation
//
// EvaluateCAA previously hand-parsed one specific hardcoded expression
// shape with string-splitting and called it a "simulated CEL Engine" --
// any policy stored with different wording or structure (e.g. "&&" order
// swapped, a different device field) would silently fall through to the
// device-only check. This compiles and evaluates the actual stored
// cel_expression with github.com/google/cel-go, declaring the same
// device/request variables the policies reference plus a custom
// in_subnet(ip, cidr) function.

var celEnv *cel.Env

func init() {
	env, err := cel.NewEnv(
		cel.Variable("device", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("request", cel.MapType(cel.StringType, cel.DynType)),
		cel.Function("in_subnet",
			cel.Overload("in_subnet_string_string",
				[]*cel.Type{cel.StringType, cel.StringType},
				cel.BoolType,
				cel.BinaryBinding(celInSubnet),
			),
		),
	)
	if err != nil {
		// A broken CEL environment is a programming error, not a runtime
		// condition any caller can recover from -- fail at startup like any
		// other dependency wiring mistake.
		panic(fmt.Sprintf("failed to build CEL environment: %v", err))
	}
	celEnv = env
}

func celInSubnet(ipVal, cidrVal ref.Val) ref.Val {
	ipStr, ok := ipVal.Value().(string)
	if !ok {
		return types.Bool(false)
	}
	cidrStr, ok := cidrVal.Value().(string)
	if !ok {
		return types.Bool(false)
	}
	ip := net.ParseIP(ipStr)
	_, subnet, err := net.ParseCIDR(cidrStr)
	if ip == nil || err != nil {
		return types.Bool(false)
	}
	return types.Bool(subnet.Contains(ip))
}

// evaluateCELPolicy compiles and runs expression against the device/request
// context. Returns (result, error); any compile or runtime error is secure-fail
// (false) with the error returned for logging, never a permissive default.
func evaluateCELPolicy(expression string, ctx RequestContext) (bool, error) {
	ast, issues := celEnv.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("CEL compile error: %w", issues.Err())
	}

	program, err := celEnv.Program(ast)
	if err != nil {
		return false, fmt.Errorf("CEL program build error: %w", err)
	}

	out, _, err := program.Eval(map[string]interface{}{
		"device": map[string]interface{}{
			"is_managed":          ctx.IsManaged,
			"screen_lock_enabled": ctx.ScreenLockEnabled,
		},
		"request": map[string]interface{}{
			"ip": ctx.IP,
		},
	})
	if err != nil {
		return false, fmt.Errorf("CEL eval error: %w", err)
	}

	result, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression did not evaluate to a boolean, got %T", out.Value())
	}
	return result, nil
}
