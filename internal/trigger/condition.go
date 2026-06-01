package trigger

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Op represents a comparison operator.
type Op int

const (
	OpEq  Op = iota // ==
	OpNeq           // !=
	OpGt            // >
	OpLt            // <
	OpGte           // >=
	OpLte           // <=
)

// Condition represents a single key-operator-value comparison.
// If ValueIsKey is true, Value names another store key whose current value
// is looked up at evaluation time.
type Condition struct {
	Key        string
	Op         Op
	Value      string
	ValueIsKey bool
}

// operators ordered longest-first so ">=" is tried before ">".
var operators = []struct {
	text string
	op   Op
}{
	{">=", OpGte},
	{"<=", OpLte},
	{"!=", OpNeq},
	{"==", OpEq},
	{">", OpGt},
	{"<", OpLt},
}

// storeKeyRe matches dotted identifiers like "ecobee.inside_temp" or
// "tempest.temp_f". RHS values that match this are treated as store-key
// references rather than literals.
var storeKeyRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*(\.[a-zA-Z_][a-zA-Z0-9_]*)+$`)

// ParseCondition parses a "key op value" expression string. If the value
// looks like another store key (dotted identifier, not numeric), the
// resulting Condition is flagged so evaluation resolves it from the store.
func ParseCondition(expr string) (Condition, error) {
	expr = strings.TrimSpace(expr)
	for _, o := range operators {
		if idx := strings.Index(expr, o.text); idx > 0 {
			key := strings.TrimSpace(expr[:idx])
			val := strings.TrimSpace(expr[idx+len(o.text):])
			if key == "" || val == "" {
				return Condition{}, fmt.Errorf("empty key or value in %q", expr)
			}
			c := Condition{Key: key, Op: o.op, Value: val}
			if _, err := strconv.ParseFloat(val, 64); err != nil && storeKeyRe.MatchString(val) {
				c.ValueIsKey = true
			}
			return c, nil
		}
	}
	return Condition{}, fmt.Errorf("no operator found in %q (use ==, !=, >, <, >=, <=)", expr)
}

// Evaluate checks the condition against a single LHS value, treating
// Value as a literal. Returns false if the key was not found.
func (c Condition) Evaluate(storeValue string, found bool) bool {
	if !found {
		return false
	}
	return c.compare(storeValue, c.Value)
}

// EvaluateWith resolves both LHS and (optionally) RHS through getter.
// Returns false if either side's key is missing.
func (c Condition) EvaluateWith(getter func(string) (string, bool)) bool {
	lhs, ok := getter(c.Key)
	if !ok {
		return false
	}
	rhs := c.Value
	if c.ValueIsKey {
		v, ok := getter(c.Value)
		if !ok {
			return false
		}
		rhs = v
	}
	return c.compare(lhs, rhs)
}

func (c Condition) compare(lhs, rhs string) bool {
	sv, errSV := strconv.ParseFloat(lhs, 64)
	cv, errCV := strconv.ParseFloat(rhs, 64)
	if errSV == nil && errCV == nil {
		return compareNumeric(sv, cv, c.Op)
	}
	return compareString(lhs, rhs, c.Op)
}

func compareNumeric(a, b float64, op Op) bool {
	switch op {
	case OpEq:
		return a == b
	case OpNeq:
		return a != b
	case OpGt:
		return a > b
	case OpLt:
		return a < b
	case OpGte:
		return a >= b
	case OpLte:
		return a <= b
	}
	return false
}

func compareString(a, b string, op Op) bool {
	cmp := strings.Compare(a, b)
	switch op {
	case OpEq:
		return cmp == 0
	case OpNeq:
		return cmp != 0
	case OpGt:
		return cmp > 0
	case OpLt:
		return cmp < 0
	case OpGte:
		return cmp >= 0
	case OpLte:
		return cmp <= 0
	}
	return false
}

// EvaluateAll returns true only if all conditions are met.
// Returns false if the conditions slice is empty.
func EvaluateAll(conditions []Condition, getter func(string) (string, bool)) bool {
	if len(conditions) == 0 {
		return false
	}
	for _, c := range conditions {
		if !c.EvaluateWith(getter) {
			return false
		}
	}
	return true
}
