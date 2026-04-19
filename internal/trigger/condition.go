package trigger

import (
	"fmt"
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
type Condition struct {
	Key   string
	Op    Op
	Value string
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

// ParseCondition parses a "key op value" expression string.
func ParseCondition(expr string) (Condition, error) {
	expr = strings.TrimSpace(expr)
	for _, o := range operators {
		if idx := strings.Index(expr, o.text); idx > 0 {
			key := strings.TrimSpace(expr[:idx])
			val := strings.TrimSpace(expr[idx+len(o.text):])
			if key == "" || val == "" {
				return Condition{}, fmt.Errorf("empty key or value in %q", expr)
			}
			return Condition{Key: key, Op: o.op, Value: val}, nil
		}
	}
	return Condition{}, fmt.Errorf("no operator found in %q (use ==, !=, >, <, >=, <=)", expr)
}

// Evaluate checks the condition against a store value.
// Returns false if the key was not found in the store.
func (c Condition) Evaluate(storeValue string, found bool) bool {
	if !found {
		return false
	}

	// Try numeric comparison first
	sv, errSV := strconv.ParseFloat(storeValue, 64)
	cv, errCV := strconv.ParseFloat(c.Value, 64)
	if errSV == nil && errCV == nil {
		return compareNumeric(sv, cv, c.Op)
	}

	// Fall back to string comparison
	return compareString(storeValue, c.Value, c.Op)
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
		val, found := getter(c.Key)
		if !c.Evaluate(val, found) {
			return false
		}
	}
	return true
}
