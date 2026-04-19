package trigger

import "testing"

func TestParseCondition(t *testing.T) {
	tests := []struct {
		expr    string
		wantKey string
		wantOp  Op
		wantVal string
	}{
		{"tempest.temp_f > 90", "tempest.temp_f", OpGt, "90"},
		{"tempest.temp_f < 32", "tempest.temp_f", OpLt, "32"},
		{"tempest.temp_f >= 90", "tempest.temp_f", OpGte, "90"},
		{"tempest.temp_f <= 32", "tempest.temp_f", OpLte, "32"},
		{"ecobee.saved_mode == heat", "ecobee.saved_mode", OpEq, "heat"},
		{"flow.flowing != true", "flow.flowing", OpNeq, "true"},
		{"  key  ==  value  ", "key", OpEq, "value"},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			c, err := ParseCondition(tt.expr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.Key != tt.wantKey {
				t.Errorf("key = %q, want %q", c.Key, tt.wantKey)
			}
			if c.Op != tt.wantOp {
				t.Errorf("op = %d, want %d", c.Op, tt.wantOp)
			}
			if c.Value != tt.wantVal {
				t.Errorf("value = %q, want %q", c.Value, tt.wantVal)
			}
		})
	}
}

func TestParseCondition_Invalid(t *testing.T) {
	tests := []string{
		"",
		"no_operator_here",
		"== value",
		"key ==",
		"key",
	}
	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			_, err := ParseCondition(expr)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestCondition_NumericComparison(t *testing.T) {
	tests := []struct {
		name  string
		store string
		op    Op
		value string
		want  bool
	}{
		{"90.5 > 90", "90.5", OpGt, "90", true},
		{"89 > 90", "89", OpGt, "90", false},
		{"90 >= 90", "90", OpGte, "90", true},
		{"32.0 == 32", "32.0", OpEq, "32", true},
		{"32 != 33", "32", OpNeq, "33", true},
		{"31 < 32", "31", OpLt, "32", true},
		{"-5 < 0", "-5", OpLt, "0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Condition{Key: "k", Op: tt.op, Value: tt.value}
			got := c.Evaluate(tt.store, true)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCondition_StringComparison(t *testing.T) {
	tests := []struct {
		name  string
		store string
		op    Op
		value string
		want  bool
	}{
		{"heat == heat", "heat", OpEq, "heat", true},
		{"heat == cool", "heat", OpEq, "cool", false},
		{"heat != cool", "heat", OpNeq, "cool", true},
		{"true == true", "true", OpEq, "true", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Condition{Key: "k", Op: tt.op, Value: tt.value}
			got := c.Evaluate(tt.store, true)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCondition_NumericParseFallback(t *testing.T) {
	// One side can't parse as float — falls back to string comparison
	c := Condition{Key: "k", Op: OpGt, Value: "90"}
	got := c.Evaluate("abc", true)
	// "abc" > "90" is lexicographic: 'a' > '9' is true
	if !got {
		t.Error("expected true for lexicographic 'abc' > '90'")
	}
}

func TestCondition_KeyNotFound(t *testing.T) {
	c := Condition{Key: "missing", Op: OpEq, Value: "anything"}
	if c.Evaluate("", false) {
		t.Error("should return false when key not found")
	}
}

func TestEvaluateAll_AllMet(t *testing.T) {
	store := map[string]string{
		"temp": "95",
		"mode": "heat",
	}
	getter := func(k string) (string, bool) {
		v, ok := store[k]
		return v, ok
	}
	conds := []Condition{
		{Key: "temp", Op: OpGt, Value: "90"},
		{Key: "mode", Op: OpEq, Value: "heat"},
	}
	if !EvaluateAll(conds, getter) {
		t.Error("expected true when all conditions met")
	}
}

func TestEvaluateAll_OneFails(t *testing.T) {
	store := map[string]string{
		"temp": "85",
		"mode": "heat",
	}
	getter := func(k string) (string, bool) {
		v, ok := store[k]
		return v, ok
	}
	conds := []Condition{
		{Key: "temp", Op: OpGt, Value: "90"},
		{Key: "mode", Op: OpEq, Value: "heat"},
	}
	if EvaluateAll(conds, getter) {
		t.Error("expected false when one condition fails")
	}
}

func TestEvaluateAll_Empty(t *testing.T) {
	getter := func(k string) (string, bool) { return "", false }
	if EvaluateAll(nil, getter) {
		t.Error("expected false for empty conditions")
	}
	if EvaluateAll([]Condition{}, getter) {
		t.Error("expected false for empty conditions")
	}
}

func TestEvaluateAll_MissingKey(t *testing.T) {
	getter := func(k string) (string, bool) { return "", false }
	conds := []Condition{
		{Key: "missing", Op: OpEq, Value: "x"},
	}
	if EvaluateAll(conds, getter) {
		t.Error("expected false when key missing")
	}
}
